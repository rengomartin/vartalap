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

func TestSenderKeyIsStableAndPseudonymous(t *testing.T) {
	first := SenderKey("550e8400-e29b-41d4-a716-446655440000")
	second := SenderKey("550e8400-e29b-41d4-a716-446655440000")
	if first != second {
		t.Fatal("sender key should be stable")
	}
	if len(first) != 12 {
		t.Fatalf("sender key length = %d, want 12", len(first))
	}
	if first == SenderKey("550e8400-e29b-41d4-a716-446655440001") {
		t.Fatal("different devices must get different sender keys")
	}
}
