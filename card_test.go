package main

import (
	"strings"
	"testing"
)

func TestParseCard(t *testing.T) {
	for _, body := range []string{
		`{}`, ` {"unknown":null,"nested":{"a":1}} `,
		`{"title":"  Hello  ","description":"World","icon":"shopify","preview":true,"set":"Landing","version":"A1"}`,
		`{"title":"` + strings.Repeat("界", 60) + `","description":"` + strings.Repeat("x", 140) + `","icon":"` + strings.Repeat("x", 64) + `"}`,
		`{"title":"  ","version":" "}`,
		`{}` + strings.Repeat(" ", 4094),
	} {
		t.Run(body[:min(len(body), 60)], func(t *testing.T) {
			if _, err := parseCard(strings.NewReader(body)); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, body := range []string{
		`{} {}`, `{"title":null}`, `{"preview":null}`, `{"preview":"true"}`, `{"title":1}`, `{"set":false}`,
		`{"title":"a","title":"b"}`, `{"unknown":1,"unknown":2}`, `{"ti\u0074le":"a","title":"b"}`,
		`{"title":"a\nb"}`, `{"description":"a\u0000b"}`, `{"set":"a\u2028b"}`, `{"title":"a\u2029b"}`,
		`{"icon":"UPPER"}`, `{"icon":"a/b"}`, `{"version":"ABC"}`, `{"version":"-"}`, `{"version":"é"}`,
		`{"title":"` + strings.Repeat("界", 61) + `"}`, `{"description":"` + strings.Repeat("x", 141) + `"}`, `{"icon":"` + strings.Repeat("x", 65) + `"}`,
		"{\"title\":\"\xff\"}", `{}` + strings.Repeat(" ", 4095),
	} {
		t.Run("invalid "+body[:min(len(body), 60)], func(t *testing.T) {
			if _, err := parseCard(strings.NewReader(body)); err == nil {
				t.Fatalf("accepted %q", body)
			}
		})
	}
	card, err := parseCard(strings.NewReader(`{"title":"  Hello\t","set":" Landing ","version":" A "}`))
	if err != nil || card.Title != "Hello" || card.Set != "Landing" || card.Version != "A" {
		t.Fatalf("trimming: %+v %v", card, err)
	}
}

func TestParseCardNonObjectsAreAbsent(t *testing.T) {
	for _, body := range []string{"", " \n\t", " \n<!doctype html><title>App</title>", "null", "[]", "true", strings.Repeat("<html>", 1000)} {
		card, err := parseCard(strings.NewReader(body))
		if card != nil || err != nil {
			t.Fatalf("non-card %q: %+v %v", body[:min(len(body), 60)], card, err)
		}
	}
}

func TestCardReplacementRules(t *testing.T) {
	for _, occasion := range []string{"admission", "return", "manual"} {
		for _, result := range []string{"ok", "absent", "invalid", "status", "transport"} {
			t.Run(occasion+"/"+result, func(t *testing.T) {
				state := endpointState{}
				if occasion != "admission" {
					state.card = &previewCard{Title: "Old", Preview: true}
					state.priorPreview = true
				}
				next := &previewCard{Title: "New"}
				state.applyCard(next, result, occasion == "manual", occasion == "return")
				switch result {
				case "ok":
					if state.card != next || state.priorPreview {
						t.Fatalf("replacement: %+v", state)
					}
				case "absent", "invalid":
					if state.card != nil || state.priorPreview {
						t.Fatalf("clear: %+v", state)
					}
				default:
					if occasion == "manual" {
						if state.card == nil || state.card.Title != "Old" || !state.priorPreview {
							t.Fatalf("manual retain: %+v", state)
						}
					} else if state.card != nil || state.priorPreview != (occasion == "return") {
						t.Fatalf("recovery: %+v", state)
					}
				}
			})
		}
	}
}
