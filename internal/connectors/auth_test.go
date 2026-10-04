package connectors

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAuthStepCheck(t *testing.T) {
	prompt := func(fields ...AuthField) *AuthPrompt { return &AuthPrompt{Message: "Sign in", Fields: fields} }
	user := AuthField{Name: "username", Label: "Username", Kind: FieldText}
	for name, s := range map[string]AuthStep{
		"redirect":        {RedirectURL: "https://provider.example/authorize", Session: []byte("synthetic-verifier")},
		"prompt":          {Prompt: prompt(user, AuthField{Name: "password", Label: "Password", Kind: FieldPassword})},
		"code prompt":     {Prompt: prompt(AuthField{Name: "code", Label: "Code", Kind: FieldCode})},
		"largest session": {Prompt: prompt(user), Session: make([]byte, maxAuthSession)},
	} {
		if err := s.check(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, s := range map[string]AuthStep{
		"empty":           {},
		"both":            {RedirectURL: "https://provider.example/authorize", Prompt: prompt(user)},
		"no fields":       {Prompt: prompt()},
		"field name":      {Prompt: prompt(AuthField{Name: "User Name", Label: "Username", Kind: FieldText})},
		"no label":        {Prompt: prompt(AuthField{Name: "username", Kind: FieldText})},
		"field kind":      {Prompt: prompt(AuthField{Name: "username", Label: "Username", Kind: "email"})},
		"duplicate field": {Prompt: prompt(user, user)},
		"session size":    {Prompt: prompt(user), Session: make([]byte, maxAuthSession+1)},
	} {
		if err := s.check(); !errors.Is(err, ErrPermanent) {
			t.Errorf("%s: err = %v, want ErrPermanent", name, err)
		}
	}
}

func TestRemotePlaceholder(t *testing.T) {
	placeholder := Descriptor{Provider: "fake_sidecar", Remote: true}
	if placeholder.Available() {
		t.Fatal("a remote descriptor without streams is available")
	}
	if !(Descriptor{Provider: "withings"}).Available() {
		t.Fatal("an in-process descriptor is unavailable")
	}
	described := placeholder
	described.Version, described.AuthKind = "1", AuthNone
	described.Streams = []StreamSpec{{Name: "fake_sidecar.heart_rate", Interval: time.Hour}}
	if !described.Available() {
		t.Fatal("a described sidecar is unavailable")
	}
	if Validate(stub{Descriptor{Provider: "Bad Code", Remote: true}}, Descriptor{Provider: "Bad Code", Remote: true}) == nil {
		t.Fatal("a placeholder with an invalid provider code passed")
	}
	if Validate(stub{Descriptor{Provider: "in_process"}}, Descriptor{Provider: "in_process"}) == nil {
		t.Fatal("an in-process descriptor without streams passed")
	}

	reg, err := NewRegistry(stub{described}, stub{Descriptor{Provider: "another_sidecar", Remote: true}})
	if err != nil {
		t.Fatalf("placeholder refused: %v", err)
	}
	var codes []string
	for _, d := range reg.Descriptors() {
		codes = append(codes, d.Provider)
	}
	if strings.Join(codes, ",") != "another_sidecar,fake_sidecar" {
		t.Fatalf("descriptors not by code: %v", codes)
	}
}
