package services

import "testing"

func TestIdentitySignerRoundTrip(t *testing.T) {
	signer := NewIdentitySigner([]byte("a-test-secret-that-is-long-enough"))
	deviceID, err := NewDeviceID()
	if err != nil {
		t.Fatal(err)
	}

	token := signer.Issue(deviceID)
	got, ok := signer.Verify(token)
	if !ok || got != deviceID {
		t.Fatalf("Verify(%q) = %q, %v", token, got, ok)
	}
}

func TestIdentitySignerRejectsTampering(t *testing.T) {
	signer := NewIdentitySigner([]byte("a-test-secret-that-is-long-enough"))
	otherDevice, _ := NewDeviceID()

	cases := []string{
		"",
		"not-a-token",
		"v1." + otherDevice,
		"v2." + otherDevice + ".signature",
		"v1." + otherDevice + ".wrongsignature",
		"v1.not-a-uuid.wrongsignature",
	}
	for _, candidate := range cases {
		if _, ok := signer.Verify(candidate); ok {
			t.Fatalf("Verify(%q) should fail", candidate)
		}
	}

	// A token signed by a different server secret must not verify.
	foreign := NewIdentitySigner([]byte("another-server-secret-entirely!"))
	if _, ok := signer.Verify(foreign.Issue(otherDevice)); ok {
		t.Fatal("token signed with another secret should be rejected")
	}
}
