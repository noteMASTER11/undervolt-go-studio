package polkit_test

import (
	"encoding/xml"
	"os"
	"strings"
	"testing"
)

type policyConfig struct {
	Actions []struct {
		ID       string `xml:"id,attr"`
		Defaults struct {
			AllowAny      string `xml:"allow_any"`
			AllowInactive string `xml:"allow_inactive"`
			AllowActive   string `xml:"allow_active"`
		} `xml:"defaults"`
		Annotations []struct {
			Key   string `xml:"key,attr"`
			Value string `xml:",chardata"`
		} `xml:"annotate"`
	} `xml:"action"`
}

func TestPolicyAuthorizesOnlyInstalledHelper(t *testing.T) {
	raw, err := os.ReadFile("io.github.notemaster11.undervolt-go-studio.policy")
	if err != nil {
		t.Fatal(err)
	}
	var policy policyConfig
	if err := xml.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	if len(policy.Actions) != 1 {
		t.Fatalf("actions=%d, want 1", len(policy.Actions))
	}
	action := policy.Actions[0]
	if action.ID != "io.github.notemaster11.undervolt-go-studio.tune" {
		t.Fatalf("action id=%q", action.ID)
	}
	if action.Defaults.AllowAny != "no" || action.Defaults.AllowInactive != "no" || action.Defaults.AllowActive != "auth_admin_keep" {
		t.Fatalf("defaults=%+v", action.Defaults)
	}
	annotations := make(map[string]string, len(action.Annotations))
	for _, annotation := range action.Annotations {
		annotations[annotation.Key] = strings.TrimSpace(annotation.Value)
	}
	if got := annotations["org.freedesktop.policykit.exec.path"]; got != "/usr/libexec/undervolt-go-studio-helper" {
		t.Fatalf("helper path=%q", got)
	}
	if got := annotations["org.freedesktop.policykit.exec.allow_gui"]; got != "false" {
		t.Fatalf("allow_gui=%q", got)
	}
	text := strings.ToLower(string(raw))
	for _, forbidden := range []string{"/bin/sh", "/bin/bash", "/usr/bin/env", "python", "perl"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("policy contains forbidden interpreter %q", forbidden)
		}
	}
}
