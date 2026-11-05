package oidc

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"slices"
	"testing"
)

type accessContract struct {
	ParseCases []struct {
		Name  string `json:"name"`
		Query string `json:"query"`
		Rules []struct {
			Path   string   `json:"path"`
			Values []string `json:"values"`
		} `json:"rules"`
		Error bool `json:"error"`
	} `json:"parse_cases"`
	AllowCases []struct {
		Name   string `json:"name"`
		Query  string `json:"query"`
		Claims string `json:"claims"`
		Allow  bool   `json:"allow"`
	} `json:"allow_cases"`
}

func loadAccessContract(t *testing.T) accessContract {
	t.Helper()

	data, err := os.ReadFile("testdata/oidc-access-rule-cases.json")
	if err != nil {
		t.Fatalf("read shared access contract: %v", err)
	}

	var contract accessContract

	err = json.Unmarshal(data, &contract)
	if err != nil {
		t.Fatalf("decode shared access contract: %v", err)
	}

	return contract
}

func TestParseAccessPolicy_SharedContract(t *testing.T) {
	for _, tc := range loadAccessContract(t).ParseCases {
		t.Run(tc.Name, func(t *testing.T) {
			q, err := url.ParseQuery(tc.Query)
			if err != nil {
				t.Fatalf("parse query: %v", err)
			}

			policy, err := ParseAccessPolicy(q)
			if tc.Error {
				if err == nil {
					t.Fatalf("expected error, got %+v", policy)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseAccessPolicy: %v", err)
			}

			if len(policy.Rules) != len(tc.Rules) {
				t.Fatalf("rules = %+v, want %+v", policy.Rules, tc.Rules)
			}

			for i, want := range tc.Rules {
				got := policy.Rules[i]
				if got.Path != want.Path || !slices.Equal(got.Values, want.Values) {
					t.Errorf("rule[%d] = %+v, want %+v", i, got, want)
				}
			}
		})
	}
}

func TestAccessPolicyAllow_SharedContract(t *testing.T) {
	for _, tc := range loadAccessContract(t).AllowCases {
		t.Run(tc.Name, func(t *testing.T) {
			policy := mustPolicy(t, tc.Query)

			err := policy.Allow(decodeRawForTest(t, tc.Claims))
			if tc.Allow && err != nil {
				t.Fatalf("Allow = %v, want nil", err)
			}

			if !tc.Allow && !errors.Is(err, ErrAccessDenied) {
				t.Fatalf("Allow = %v, want ErrAccessDenied", err)
			}
		})
	}
}
