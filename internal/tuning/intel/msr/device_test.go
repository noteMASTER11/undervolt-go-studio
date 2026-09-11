package msr

import (
	"errors"
	"io/fs"
	"syscall"
	"testing"
)

func TestClassifyIOErrorUsesStableReasonCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "missing", err: fs.ErrNotExist, code: ReasonDeviceMissing},
		{name: "permission", err: fs.ErrPermission, code: ReasonPermissionDenied},
		{name: "lockdown", err: syscall.EPERM, code: ReasonKernelLockdown},
		{name: "general protection", err: syscall.EIO, code: ReasonGeneralProtection},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classified := classifyIOError(test.err)
			var reason *ReasonError
			if !errors.As(classified, &reason) || reason.Code != test.code {
				t.Fatalf("classified = %#v", classified)
			}
		})
	}
}
