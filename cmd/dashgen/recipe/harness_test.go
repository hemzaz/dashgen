// harness_test.go — shared cobra test harness for cmd/dashgen/recipe/.
//
// All recipe subcommand tests may use execCmd / mustExecCmd instead of
// hand-rolling cobra capture boilerplate. Future subcommands (show, test,
// explain, diff) should adopt this harness from the start.
package recipe

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

// execCmd runs cmd with the given args and returns the captured stdout,
// stderr, and any error returned by cmd.Execute(). The function:
//   - sets cmd.SilenceErrors = true so cobra does not print to os.Stderr
//   - sets cmd.SilenceUsage = true so usage is not emitted on error
//   - replaces cmd.SetOut / cmd.SetErr with in-memory buffers
//
// It does NOT call t.Fatal on Execute error; callers that want "must succeed"
// semantics should use mustExecCmd.
func execCmd(t *testing.T, cmd *cobra.Command, args []string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(args)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// mustExecCmd wraps execCmd and calls t.Fatalf if Execute returns a non-nil
// error. Use for test cases where failure is unexpected and continuation
// would be meaningless.
func mustExecCmd(t *testing.T, cmd *cobra.Command, args []string) (stdout, stderr string) {
	t.Helper()
	out, errStr, execErr := execCmd(t, cmd, args)
	if execErr != nil {
		t.Fatalf("execCmd(%v): unexpected error: %v\nstdout: %s\nstderr: %s",
			args, execErr, out, errStr)
	}
	return out, errStr
}
