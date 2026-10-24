package secrets

import (
	"path/filepath"
	"testing"
)

func TestSealOpenAndPersistedKey(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "secret.key")
	a, err := Load("", keyFile)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := a.Seal("sk-ant-secret")
	b, err := Load("", keyFile) // same persisted key after "restart"
	if err != nil {
		t.Fatal(err)
	}
	if got, err := b.Open(sealed); err != nil || got != "sk-ant-secret" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	other, _ := Load("different", "")
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("opening with the wrong key must fail")
	}
}
