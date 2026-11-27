package main

import (
	"flag"
	"os"
	"testing"
)

func TestClassificationFileFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, "classification.md"},
		{"custom", []string{"-classification-file", "/tmp/custom prompt.md"}, "/tmp/custom prompt.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldFlags, oldArgs := flag.CommandLine, os.Args
			t.Cleanup(func() { flag.CommandLine, os.Args = oldFlags, oldArgs })
			flag.CommandLine = flag.NewFlagSet("weblinks", flag.ContinueOnError)
			os.Args = append([]string{"weblinks"}, tc.args...)
			if got := parseFlags().classificationFile; got != tc.want {
				t.Errorf("classification file = %q, want %q", got, tc.want)
			}
		})
	}
}
