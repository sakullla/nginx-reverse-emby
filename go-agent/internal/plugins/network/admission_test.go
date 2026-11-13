package network

import (
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"testing"
)

func TestIdleProxyReadsLeaveWorkAndReclamationCapacity(t *testing.T) {
	a := NewAdmission()
	for i := 0; i < 6000; i++ {
		release, ok := a.TryAcquire(sdk.ManagedNetworkRead)
		if !ok {
			t.Fatalf("3000 duplex flows exhausted at read %d", i)
		}
		defer release()
	}
	for i := 0; i < MaxActiveOperations; i++ {
		release, ok := a.TryAcquire(sdk.ManagedNetworkWrite)
		if !ok {
			t.Fatal("idle reads consumed work capacity")
		}
		defer release()
	}
	if release, ok := a.TryAcquire(sdk.ManagedNetworkDial); ok {
		release()
		t.Fatal("work limit was not enforced")
	}
	for _, action := range []string{sdk.ManagedNetworkClose, sdk.ManagedNetworkCancel, sdk.ManagedNetworkHalfClose} {
		release, ok := a.TryAcquire(action)
		if !ok {
			t.Fatalf("saturated work blocked %s", action)
		}
		release()
	}
}

func TestReadAdmissionIsBoundedAndReusable(t *testing.T) {
	a := NewAdmission()
	releases := make([]func(), 0, MaxReadOperations)
	for i := 0; i < MaxReadOperations; i++ {
		release, ok := a.TryAcquire(sdk.ManagedNetworkReceive)
		if !ok {
			t.Fatal(i)
		}
		releases = append(releases, release)
	}
	if release, ok := a.TryAcquire(sdk.ManagedNetworkAccept); ok {
		release()
		t.Fatal("read bound was not enforced")
	}
	for _, release := range releases {
		release()
	}
	release, ok := a.TryAcquire(sdk.ManagedNetworkAccept)
	if !ok {
		t.Fatal("released read slot leaked")
	}
	release()
}
