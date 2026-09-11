package product

import "testing"

func TestCurrentReturnsStudioIdentity(t *testing.T) {
	got := Current("v0.1.0-alpha.1")
	if got.Name != "Undervolt Go Studio" {
		t.Fatalf("Name = %q", got.Name)
	}
	if got.AppID != "io.github.notemaster11.UndervoltGoStudio" {
		t.Fatalf("AppID = %q", got.AppID)
	}
	if got.Version != "v0.1.0-alpha.1" {
		t.Fatalf("Version = %q", got.Version)
	}
}
