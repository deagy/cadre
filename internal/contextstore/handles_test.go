package contextstore

import "testing"

func TestMintHandleIsWellFormed(t *testing.T) {
	h, err := MintHandle()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !IsHandle(h) {
		t.Errorf("minted handle %q does not validate as a handle", h)
	}
}

func TestMintHandleIsRandom(t *testing.T) {
	a, _ := MintHandle()
	b, _ := MintHandle()
	if a == b {
		t.Error("expected two minted handles to differ")
	}
}

func TestIsHandleRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"", "ctx_", "ctx_short", "not_a_handle",
		"ctx_" + "g123456789012345678901234567890", // 'g' is not hex
		"CTX_0123456789012345678901234567890",      // uppercase prefix
	} {
		if IsHandle(bad) {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestValidateHandleReturnsErrorForMalformed(t *testing.T) {
	if _, err := ValidateHandle("bogus"); err == nil {
		t.Fatal("expected an error for a malformed handle")
	}
}

func TestValidateHandleReturnsValueForWellFormed(t *testing.T) {
	h, _ := MintHandle()
	v, err := ValidateHandle(h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != h {
		t.Errorf("got %q, want %q", v, h)
	}
}
