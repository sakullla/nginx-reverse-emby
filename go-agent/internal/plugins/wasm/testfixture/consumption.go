// Package testfixture builds SDK conformance guests for Host integration tests.
// These probe modules are not the official IP policy product.
package testfixture

import (
	"bytes"
	"encoding/binary"
	"fmt"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/compatfixture"
	"google.golang.org/protobuf/encoding/protowire"
	"strings"
)

var testWASMHeader = []byte{0, 97, 115, 109, 1, 0, 0, 0}

type builder struct{}

func (builder) Helper()                             {}
func (builder) Fatal(values ...any)                 { panic(fmt.Errorf("%s", fmt.Sprint(values...))) }
func (builder) Fatalf(format string, values ...any) { panic(fmt.Errorf(format, values...)) }
func ConsumptionGuest(source, class string, queryOverride []byte, capacity int, initSource bool) ([]byte, error) {
	return ConsumptionGuestDecision(source, class, queryOverride, capacity, initSource, sdk.PolicyActionAllow)
}
func ConsumptionGuestDecision(source, class string, queryOverride []byte, capacity int, initSource bool, action sdk.PolicyAction) (data []byte, err error) {
	return consumptionGuestDecision(source, class, queryOverride, capacity, initSource, action, false)
}

// ConsumptionGuestMatchDecision branches inside the guest on the actual
// single-classification query response: covered match denies, non-match allows.
func ConsumptionGuestMatchDecision(source, class string, queryOverride []byte, capacity int, initSource bool) ([]byte, error) {
	if queryOverride != nil || initSource {
		return nil, fmt.Errorf("match-decision fixture requires its resolved reference and evaluation-time source")
	}
	return consumptionGuestDecision(source, class, nil, capacity, false, sdk.PolicyActionAllow, true)
}

func consumptionGuestDecision(source, class string, queryOverride []byte, capacity int, initSource bool, action sdk.PolicyAction, matchDecision bool) (data []byte, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			if value, ok := failure.(error); ok {
				err = value
				data = nil
			} else {
				panic(failure)
			}
		}
	}()
	if action != sdk.PolicyActionAllow && action != sdk.PolicyActionDeny {
		return nil, fmt.Errorf("invalid fixture action")
	}
	return buildGuest(builder{}, source, class, queryOverride, capacity, initSource, action, matchDecision), nil
}
func ResolveRequest(source string) sdk.PolicyDatasetResolveRequest {
	return sdk.PolicyDatasetResolveRequest{SourceID: source, Budget: sdk.DatasetQueryBudget{MaxDurationMicros: sdk.DatasetMaxQueryDurationMicros, MaxResponseBytes: 4096}}
}
func QueryRequest(ref sdk.DatasetReference, class string) sdk.PolicyDatasetQueryRequest {
	return sdk.PolicyDatasetQueryRequest{Reference: ref, Classifications: []sdk.DatasetClassification{{Name: class, Kind: sdk.DatasetClassificationRegion}}, Budget: sdk.DatasetQueryBudget{MaxDurationMicros: sdk.DatasetMaxQueryDurationMicros, MaxResponseBytes: 4096}}
}
func Slot(payload []byte, index int) (sdk.PolicyStatus, []byte, error) {
	if len(payload) != 3*520 || index < 0 || index >= 3 {
		return 0, nil, fmt.Errorf("invalid guest probe records")
	}
	slot := payload[index*520 : (index+1)*520]
	result := binary.LittleEndian.Uint64(slot)
	length := uint32(result)
	if length > 512 {
		return 0, nil, fmt.Errorf("oversized probe result")
	}
	return sdk.PolicyStatus(result >> 32), slot[8 : 8+length], nil
}
func buildGuest(t builder, source, class string, queryOverride []byte, queryCapacity int, sourceDuringInit bool, action sdk.PolicyAction, matchDecision bool) []byte {
	t.Helper()
	guest, err := compatfixture.PolicyV1GuestWASMWithOptionalImports(sdk.PolicyHostDatasetResolve, sdk.PolicyHostDatasetQuery, sdk.PolicyHostReadTrustedSource)
	if err != nil {
		t.Fatal(err)
	}
	resolve, err := sdk.MarshalPolicyDatasetResolveRequest(ResolveRequest(source), 4096)
	if err != nil {
		t.Fatal(err)
	}
	placeholder := sdk.DatasetReference{Handle: strings.Repeat("a", 32), InstanceID: "ip-instance", Generation: "placeholder-generation", SourceID: "regions", VersionDigest: "sha256:" + strings.Repeat("a", 64)}
	query, err := sdk.MarshalPolicyDatasetQueryRequest(QueryRequest(placeholder, class), 4096)
	if err != nil {
		t.Fatal(err)
	}
	num, wireType, tagSize := protowire.ConsumeTag(query)
	if num != 1 || wireType != protowire.BytesType {
		t.Fatal("canonical dataset reference is no longer query field 1")
	}
	_, fieldSize := protowire.ConsumeBytes(query[tagSize:])
	tail := query[tagSize+fieldSize:]
	payload := make([]byte, 3*520)
	success := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), uint64(action))
	success = protowire.AppendBytes(protowire.AppendTag(success, 2, protowire.BytesType), payload)
	response := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), success)
	const responseOffset, queryOffset, resolveOffset, tailOffset = 512, 2200, 2800, 3000
	firstSlot := responseOffset + len(response) - len(payload)
	const32 := func(code []byte, value int) []byte { return consumptionSLEB(append(code, 0x41), int64(value)) }
	call := func(code []byte, index, request, length, slot, capacity int) []byte {
		code = const32(code, slot)
		code = const32(code, request)
		code = const32(code, length)
		code = const32(code, slot+8)
		code = const32(code, capacity)
		return append(code, 0x10, byte(index), 0x37, 0x03, 0x00)
	}
	init := []byte{0} // no locals
	init = call(init, 6, resolveOffset, len(resolve), firstSlot, 512)
	init = const32(init, firstSlot)
	init = append(init, 0x29, 0x03, 0x00, 0x42, 0x20, 0x88, 0xa7, 0x04, 0x40, 0x41, 0x01, 0x0f, 0x0b)
	if sourceDuringInit {
		init = call(init, 8, 0, 0, firstSlot+2*520, 512)
		init = const32(init, firstSlot+2*520)
		init = append(init, 0x29, 0x03, 0x00, 0x42, 0x20, 0x88, 0xa7, 0x0f)
	} else if queryOverride == nil {
		// memory.copy(query, resolve response, actual response length).
		init = const32(init, queryOffset)
		init = const32(init, firstSlot+8)
		init = const32(init, firstSlot)
		init = append(init, 0x28, 0x02, 0x00, 0xfc, 0x0a, 0, 0)
		init = const32(init, queryOffset)
		init = const32(init, firstSlot)
		init = append(init, 0x28, 0x02, 0x00, 0x6a)
		init = const32(init, tailOffset)
		init = const32(init, len(tail))
		init = append(init, 0xfc, 0x0a, 0, 0)
	}
	init = append(init, 0x41, 0, 0x0b)
	evaluate := []byte{0}
	evaluate = const32(evaluate, firstSlot+520)
	evaluate = const32(evaluate, queryOffset)
	if queryOverride == nil {
		evaluate = const32(evaluate, firstSlot)
		evaluate = append(evaluate, 0x28, 0x02, 0x00)
		evaluate = const32(evaluate, len(tail))
		evaluate = append(evaluate, 0x6a)
	} else {
		evaluate = const32(evaluate, len(queryOverride))
	}
	evaluate = const32(evaluate, firstSlot+520+8)
	evaluate = const32(evaluate, queryCapacity)
	evaluate = append(evaluate, 0x10, 7, 0x37, 0x03, 0)
	evaluate = call(evaluate, 8, 0, 0, firstSlot+2*520, 512)
	if matchDecision {
		actionOffset := responseOffset + len(response) - len(success) + 1
		evaluate = appendMatchDecision(t, evaluate, placeholder, class, firstSlot, actionOffset)
	}
	// Preserve the fixture's exported free/reset allocation contract.
	evaluate = const32(evaluate, responseOffset)
	evaluate = append(evaluate, 0x24, 4)
	evaluate = const32(evaluate, len(response))
	evaluate = append(evaluate, 0x24, 5, 0x41, 1, 0x24, 6)
	evaluate = consumptionSLEB(append(evaluate, 0x42), int64(uint64(responseOffset)<<32|uint64(len(response))))
	evaluate = append(evaluate, 0x0b)
	guest = rewritePolicyFixtureSection(t, guest, 10, func(section []byte) []byte {
		count, consumed, ok := consumeTestULEB32(section)
		if !ok || count != 6 {
			t.Fatal("unexpected SDK guest function vector")
		}
		result, rest := appendTestULEB32(nil, count), section[consumed:]
		for i := 0; i < int(count); i++ {
			length, prefix, ok := consumeTestULEB32(rest)
			if !ok || int(length) > len(rest)-prefix {
				t.Fatal("invalid guest code body")
			}
			body := rest[prefix : prefix+int(length)]
			rest = rest[prefix+int(length):]
			if i == 3 {
				body = init
			}
			if i == 4 {
				body = evaluate
			}
			result = append(appendTestULEB32(result, uint32(len(body))), body...)
		}
		return result
	})
	return rewritePolicyFixtureSection(t, guest, 11, func([]byte) []byte {
		segments := []struct {
			offset int
			data   []byte
		}{{responseOffset, response}, {resolveOffset, resolve}, {tailOffset, tail}}
		if queryOverride != nil {
			segments = append(segments, struct {
				offset int
				data   []byte
			}{queryOffset, queryOverride})
		}
		result := appendTestULEB32(nil, uint32(len(segments)))
		for _, segment := range segments {
			result = const32(append(result, 0), segment.offset)
			result = append(result, 0x0b)
			result = append(appendTestULEB32(result, uint32(len(segment.data))), segment.data...)
		}
		return result
	})
}

func appendMatchDecision(t builder, code []byte, reference sdk.DatasetReference, class string, firstSlot, actionOffset int) []byte {
	// The SDK's deterministic wire format starts both resolve and query
	// responses with the same reference field. Use the actual resolve length,
	// never a guessed reference size, to find status + the sole index-zero match.
	// Assert this probe's deliberately bounded wire contract using public SDK
	// encoding. Unknown status/coverage/layout traps instead of inventing allow.
	matched := []byte{0x10, 1, 0x1a, 4, 0x10, 1, 0x18, 1}
	unmatched := []byte{0x10, 1, 0x1a, 2, 0x18, 1}
	for _, value := range []bool{false, true} {
		wire, err := sdk.MarshalPolicyDatasetQueryResponse(sdk.DatasetQueryResponse{Reference: reference, Status: sdk.DatasetQueryOK, Matches: []sdk.DatasetMatch{{Index: 0, Matched: value, Coverage: sdk.DatasetCovered}}}, QueryRequest(reference, class))
		if err != nil {
			t.Fatal(err)
		}
		_, _, tagSize := protowire.ConsumeTag(wire)
		_, fieldSize := protowire.ConsumeBytes(wire[tagSize:])
		want := unmatched
		if value {
			want = matched
		}
		if tagSize < 0 || fieldSize < 0 || !bytes.Equal(wire[tagSize+fieldSize:], want) {
			t.Fatal("SDK single-match response layout changed")
		}
	}
	const32 := func(code []byte, value int) []byte { return consumptionSLEB(append(code, 0x41), int64(value)) }
	load32 := func(code []byte, address int) []byte { return append(const32(code, address), 0x28, 0x02, 0) }
	suffixAddress := func(code []byte, extra int) []byte {
		code = const32(code, firstSlot+520+8+extra)
		code = load32(code, firstSlot)
		return append(code, 0x6a) // i32.add
	}
	suffixLength := func(code []byte) []byte {
		code = load32(code, firstSlot+520)
		code = load32(code, firstSlot)
		return append(code, 0x6b) // i32.sub
	}
	// Non-OK import status is not a dataset miss.
	code = const32(code, firstSlot+520)
	code = append(code, 0x29, 0x03, 0, 0x42, 0x20, 0x88, 0xa7, 0x04, 0x40, 0x00, 0x0b)
	code = const32(code, actionOffset)
	code = const32(suffixLength(code), len(matched))
	code = append(code, 0x46, 0x04, 0x7f) // length == 8; if result i32
	code = append(suffixAddress(code, 0), 0x29, 0, 0)
	code = consumptionSLEB(append(code, 0x42), int64(binary.LittleEndian.Uint64(matched)))
	code = append(code, 0x52, 0x04, 0x40, 0x00, 0x0b) // unexpected bytes trap
	code = const32(code, int(sdk.PolicyActionDeny))
	code = append(code, 0x05) // else
	code = const32(suffixLength(code), len(unmatched))
	code = append(code, 0x47, 0x04, 0x40, 0x00, 0x0b)
	code = append(suffixAddress(code, 0), 0x28, 0, 0)
	code = const32(code, int(binary.LittleEndian.Uint32(unmatched)))
	code = append(code, 0x47, 0x04, 0x40, 0x00, 0x0b)
	code = append(suffixAddress(code, 4), 0x2f, 0, 0) // i32.load16_u
	code = const32(code, int(binary.LittleEndian.Uint16(unmatched[4:])))
	code = append(code, 0x47, 0x04, 0x40, 0x00, 0x0b)
	code = const32(code, int(sdk.PolicyActionAllow))
	return append(code, 0x0b, 0x3a, 0, 0) // end; i32.store8
}

func consumptionSLEB(dst []byte, value int64) []byte {
	for {
		b := byte(value & 0x7f)
		value >>= 7
		done := (value == 0 && b&0x40 == 0) || (value == -1 && b&0x40 != 0)
		if !done {
			b |= 0x80
		}
		dst = append(dst, b)
		if done {
			return dst
		}
	}
}
func rewritePolicyFixtureSection(t builder, module []byte, targetID byte, rewrite func([]byte) []byte) []byte {
	t.Helper()
	result := append([]byte(nil), testWASMHeader...)
	remaining := module[len(testWASMHeader):]
	found := false
	for len(remaining) > 0 {
		sectionID := remaining[0]
		length, consumed, ok := consumeTestULEB32(remaining[1:])
		if !ok || int(length) > len(remaining)-1-consumed {
			t.Fatal("malformed compatibility fixture section")
		}
		section := append([]byte(nil), remaining[1+consumed:1+consumed+int(length)]...)
		remaining = remaining[1+consumed+int(length):]
		if sectionID == targetID {
			section = rewrite(section)
			found = true
		}
		result = append(result, sectionID)
		result = appendTestULEB32(result, uint32(len(section)))
		result = append(result, section...)
	}
	if !found {
		t.Fatalf("compatibility fixture section %d is missing", targetID)
	}
	return result
}

func consumeTestULEB32(encoded []byte) (uint32, int, bool) {
	var result uint32
	for index := 0; index < len(encoded) && index < 5; index++ {
		current := encoded[index]
		if index == 4 && current&0xf0 != 0 {
			return 0, 0, false
		}
		result |= uint32(current&0x7f) << (7 * index)
		if current&0x80 == 0 {
			return result, index + 1, true
		}
	}
	return 0, 0, false
}

func appendTestULEB32(target []byte, value uint32) []byte {
	var encoded [binary.MaxVarintLen32]byte
	length := binary.PutUvarint(encoded[:], uint64(value))
	return append(target, encoded[:length]...)
}
