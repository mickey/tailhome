package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParsePreviewPorts(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  []uint16
	}{
		{"", []uint16{}}, {"  ", []uint16{}}, {" 8443-8445 , 8444,1,65535 ", []uint16{1, 8443, 8444, 8445, 65535}},
		{"1000-1031", func() []uint16 {
			p := []uint16{}
			for i := uint16(1000); i <= 1031; i++ {
				p = append(p, i)
			}
			return p
		}()},
	} {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parsePreviewPorts(tt.input)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
	for _, input := range []string{",", "1,", "1,,2", "443", "442-444", "0", "65536", "-1", "+1", "1-0", "2-1", "1-2-3", "1 -2", "1- 2", "1.2", "1e2", "1000-1032", "1-32,34", strings.Repeat("9", 100)} {
		t.Run("invalid "+input, func(t *testing.T) {
			if _, err := parsePreviewPorts(input); err == nil {
				t.Fatalf("accepted %q", input)
			}
		})
	}
}
