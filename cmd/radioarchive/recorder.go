package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/store"
)

const recorderUsage = `usage:
  radioarchive recorder add "<name>"
  radioarchive recorder list
  radioarchive recorder rotate-key <name>
  radioarchive recorder retire <name>`

// recorderCommand runs a recorder shell command (SPEC.md R10). Keys are only
// ever shown here, once, and never stored.
func recorderCommand(ctx context.Context, st *store.Store, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(recorderUsage)
	}
	one := func() (string, error) {
		if len(args) != 2 {
			return "", errors.New(recorderUsage)
		}
		return args[1], nil
	}
	switch args[0] {
	case "add":
		name, err := one()
		if err != nil {
			return err
		}
		key, err := st.AddRecorder(ctx, name)
		if err != nil {
			return err
		}
		printKey(out, name, key, "created")
		return nil
	case "rotate-key":
		name, err := one()
		if err != nil {
			return err
		}
		key, err := st.RotateRecorderKey(ctx, name)
		if err != nil {
			return err
		}
		printKey(out, name, key, "has a new key; the old one no longer works")
		return nil
	case "retire":
		name, err := one()
		if err != nil {
			return err
		}
		if err := st.RetireRecorderByName(ctx, name); err != nil {
			return err
		}
		fmt.Fprintf(out, "Recorder %q retired: its key no longer works. Its calls stay until retention.\n", name)
		return nil
	case "list":
		if len(args) != 1 {
			return errors.New(recorderUsage)
		}
		rs, err := st.Recorders(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSTATE\tCREATED\tKEY ROTATED")
		for _, r := range rs {
			state := "active"
			if r.RetiredAt != nil {
				state = "retired " + r.RetiredAt.UTC().Format(time.RFC3339)
			}
			rotated := "-"
			if r.KeyRotatedAt != nil {
				rotated = r.KeyRotatedAt.UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, state, r.CreatedAt.UTC().Format(time.RFC3339), rotated)
		}
		return tw.Flush()
	default:
		return errors.New(recorderUsage)
	}
}

func printKey(out io.Writer, name, key, what string) {
	fmt.Fprintf(out, "Recorder %q %s.\n\nClient key (shown once, only its hash is kept):\n\n  %s\n\n"+
		"Give it to the recorder client as CLIENT_KEY.\n", name, what, key)
}
