package linuxfs

import "testing"

func TestRootFSRejectsParentTraversal(t *testing.T) {
	filesystem := RootFS{Root: t.TempDir()}
	if _, err := filesystem.ReadFile("../outside"); err == nil {
		t.Fatal("parent traversal was accepted")
	}
}
