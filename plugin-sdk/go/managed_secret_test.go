package pluginsdk

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func managedTestSecretReference() ScopedSecretReference {
	return ScopedSecretReference{InstanceID: "instance-a", ID: "upstream-1", Version: strings.Repeat("v", 32), Scope: "outbound-auth"}
}
func managedTestSecret(t *testing.T) *ManagedSecretMaterial {
	t.Helper()
	material, err := NewManagedSecretMaterial([]byte("sensitive-material"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(material.Close)
	return material
}

func TestManagedSecretRoundTripRotateAndRevoke(t *testing.T) {
	material := managedTestSecret(t)
	current := managedTestSecretReference()
	record := &ScopedSecretRecord{Reference: current, Active: true}
	client := managedTestClient(t, func(_ *http.Request, call HostRuntimeCall) HostRuntimeResponse {
		if call.Operation != HostRuntimeScopedSecret {
			t.Fatal("wrong operation")
		}
		request, err := DecodeScopedSecretRequest(call.Payload)
		if err != nil {
			t.Fatal(err)
		}
		defer request.Material.Close()
		if err := ValidateScopedSecretBinding(request, managedTestBinding(), record, []string{PermissionScopedSecretRead, PermissionScopedSecretWrite}, []string{"outbound-auth"}); err != nil {
			return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorPermissionDenied, Message: "denied"}}
		}
		response := ScopedSecretResponse{Reference: record.Reference}
		switch request.Action {
		case ScopedSecretRead:
			response.Material = material
		case ScopedSecretRotate:
			response.Reference.Version = strings.Repeat("w", 32)
			record.Reference = response.Reference
		case ScopedSecretRevoke:
			record.Active = false
			response.Revoked = true
		}
		payload, err := EncodeScopedSecretResponse(request, response)
		if err != nil {
			t.Fatal(err)
		}
		return HostRuntimeResponse{Payload: payload}
	})
	request := ScopedSecretRequest{Action: ScopedSecretRead, Binding: managedTestBinding(), Reference: current}
	response, err := client.ScopedSecret(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Material.Close()
	if err := response.Material.WithBytes(func(value []byte) error {
		if string(value) != "sensitive-material" {
			t.Fatal("material was not delivered")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request.Action, request.Material = ScopedSecretRotate, material
	rotated, err := client.ScopedSecret(t.Context(), request)
	if err != nil || rotated.Reference.Version == current.Version {
		t.Fatalf("rotate = %+v %v", rotated, err)
	}
	request.Action, request.Material = ScopedSecretRead, nil
	if _, err := client.ScopedSecret(t.Context(), request); err == nil {
		t.Fatal("old version delivered after rotate")
	}
	request.Reference = rotated.Reference
	request.Action = ScopedSecretRevoke
	if _, err := client.ScopedSecret(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	request.Action = ScopedSecretRead
	if _, err := client.ScopedSecret(t.Context(), request); err == nil {
		t.Fatal("revoked version delivered")
	}
}

func TestManagedSecretMaterialCannotLeakThroughFormattingOrState(t *testing.T) {
	material := managedTestSecret(t)
	request := ScopedSecretRequest{Action: ScopedSecretRotate, Binding: managedTestBinding(), Reference: managedTestSecretReference(), Material: material}
	for _, value := range []any{material, *material, request, map[string]any{"credential": material}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			printed := fmt.Sprintf(format, value)
			if strings.Contains(printed, "sensitive-material") || strings.Contains(printed, "115 101 110 115") {
				t.Fatal("secret leaked through formatting")
			}
		}
		if encoded, err := json.Marshal(value); err == nil {
			t.Fatalf("ordinary state serialization succeeded: %s", encoded)
		}
	}
	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("credential", "material", material)
	if strings.Contains(logged.String(), "sensitive-material") || !strings.Contains(logged.String(), "REDACTED") {
		t.Fatal("secret log was not redacted")
	}
	var borrowed []byte
	if err := material.WithBytes(func(value []byte) error { borrowed = value; return nil }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(borrowed, make([]byte, len(borrowed))) {
		t.Fatal("callback bytes were not wiped")
	}
	copy := *material
	material.Close()
	if copy.WithBytes(func([]byte) error { t.Fatal("closed material accessible"); return nil }) == nil {
		t.Fatal("revoked material read succeeded")
	}
}

func TestManagedSecretBindingAndBoundaries(t *testing.T) {
	request := ScopedSecretRequest{Action: ScopedSecretRead, Binding: managedTestBinding(), Reference: managedTestSecretReference()}
	record := ScopedSecretRecord{Reference: request.Reference, Active: true}
	for _, variant := range []string{"instance", "generation", "entry", "scope", "grant", "version", "revoked"} {
		t.Run(variant, func(t *testing.T) {
			caller, changed := managedTestBinding(), record
			grants, scopes := []string{PermissionScopedSecretRead}, []string{"outbound-auth"}
			switch variant {
			case "instance":
				caller.InstanceID = "other"
			case "generation":
				caller.Generation = "old"
			case "entry":
				caller.EntryID = "other"
			case "scope":
				scopes = nil
			case "grant":
				grants = nil
			case "version":
				changed.Reference.Version = strings.Repeat("z", 32)
			case "revoked":
				changed.Active = false
			}
			if ValidateScopedSecretBinding(request, caller, &changed, grants, scopes) == nil {
				t.Fatal("invalid secret authority accepted")
			}
		})
	}
	for _, value := range [][]byte{nil, make([]byte, ManagedSecretMaxBytes+1)} {
		if _, err := NewManagedSecretMaterial(value); err == nil {
			t.Fatal("invalid secret size accepted")
		}
	}
	request.Action, request.Material = ScopedSecretCreate, managedTestSecret(t)
	request.Reference.Version = ""
	payload, err := EncodeScopedSecretRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeScopedSecretRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	defer decoded.Material.Close()
	if err := ValidateScopedSecretBinding(decoded, managedTestBinding(), nil, []string{PermissionScopedSecretWrite}, []string{"outbound-auth"}); err != nil {
		t.Fatal(err)
	}
	if ValidateScopedSecretBinding(decoded, managedTestBinding(), &record, []string{PermissionScopedSecretWrite}, []string{"outbound-auth"}) == nil {
		t.Fatal("create overwrote existing secret")
	}
	for _, bad := range []string{
		string(payload[:len(payload)-1]) + `,"trusted":true}`,
		string(payload) + ` {}`,
		strings.Replace(string(payload), base64.StdEncoding.EncodeToString([]byte("sensitive-material")), base64.StdEncoding.EncodeToString(make([]byte, ManagedSecretMaxBytes+1)), 1),
	} {
		if decoded, err := DecodeScopedSecretRequest(json.RawMessage(bad)); err == nil {
			decoded.Material.Close()
			t.Fatal("invalid secret payload accepted")
		}
	}
}

func TestManagedSecretResponseValidationAndErrorRedaction(t *testing.T) {
	request := ScopedSecretRequest{Action: ScopedSecretRead, Binding: managedTestBinding(), Reference: managedTestSecretReference()}
	response := ScopedSecretResponse{Reference: request.Reference, Material: managedTestSecret(t)}
	response.Reference.Version = strings.Repeat("z", 32)
	if response.ValidateFor(request) == nil {
		t.Fatal("wrong delivered version accepted")
	}
	client := managedTestClient(t, func(_ *http.Request, _ HostRuntimeCall) HostRuntimeResponse {
		return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorPermissionDenied, Message: "sensitive-material"}}
	})
	_, err := client.ScopedSecret(t.Context(), request)
	var failure *RuntimeError
	if !errors.As(err, &failure) || failure.Code != ErrorPermissionDenied || strings.Contains(err.Error(), "sensitive-material") {
		t.Fatalf("unsafe or lost error classification: %v", err)
	}
}
