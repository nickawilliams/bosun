package slack

import "testing"

// removePKCS5Padding is the last step of decrypting the local Slack cookie.
// Every rejection branch matters: accepting malformed padding would hand a
// silently-corrupted token to the caller, which surfaces later as an opaque
// auth failure rather than a decryption error.
func TestRemovePKCS5Padding(t *testing.T) {
	t.Run("strips valid padding", func(t *testing.T) {
		// Three bytes of value 0x03 is well-formed PKCS#5/#7 padding.
		got, err := removePKCS5Padding([]byte{'a', 'b', 3, 3, 3})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(got) != "ab" {
			t.Errorf("got %q, want %q", got, "ab")
		}
	})

	t.Run("padding spanning the whole block", func(t *testing.T) {
		in := make([]byte, 16)
		for i := range in {
			in[i] = 16
		}
		got, err := removePKCS5Padding(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d bytes, want 0", len(got))
		}
	})

	rejects := []struct {
		name string
		in   []byte
	}{
		{"empty input", []byte{}},
		{"zero padding value", []byte{'a', 0}},
		{"padding larger than the block size", []byte{'a', 17}},
		{"padding longer than the data", []byte{2, 3}},
		{"padding bytes disagree", []byte{'a', 3, 2, 3}},
	}
	for _, tc := range rejects {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			if _, err := removePKCS5Padding(tc.in); err == nil {
				t.Errorf("removePKCS5Padding(%v) = nil error, want rejection", tc.in)
			}
		})
	}
}
