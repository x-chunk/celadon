package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/output"
)

// maxBody bounds a shortcut body read from a file; the API holds bodies to a
// far smaller ceiling of its own.
const maxBody = 64 << 10

func newActionsCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "actions",
		Aliases: []string{"action"},
		Short:   "Manage the shortcuts the bot expands in your chats",
		Long: `Manage the account's shortcuts: a name typed behind the prefix (".kiss") that
the bot replaces with a body of its own, placeholders and all.

` + "`celadon actions placeholders`" + ` lists the placeholders a body may carry and whether
the plan opens them; a body using one the plan does not open is refused.`,
		GroupID: groupAccount,
	}
	cmd.AddCommand(
		newActionsListCmd(env),
		newActionsViewCmd(env),
		newActionsCreateCmd(env),
		newActionsEditCmd(env),
		newActionsDeleteCmd(env),
		newActionsPlaceholdersCmd(env),
	)
	return cmd
}

func newActionsListCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every shortcut",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			list, meta, err := c.Actions.List(ctx)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(list, func() error {
				if len(list.Actions) == 0 {
					p.Info("No shortcuts yet. Create one with `celadon actions create <name>`.")
				} else {
					bodyWidth := 0
					if w := env.IO.Width(); w > 0 {
						bodyWidth = max(w-56, 20)
					}
					rows := make([][]string, 0, len(list.Actions))
					for _, a := range list.Actions {
						body := output.OneLine(a.Body)
						if bodyWidth > 0 {
							body = output.Truncate(body, bodyWidth)
						}
						rows = append(rows, []string{
							strconv.FormatInt(a.ID, 10), list.Prefix + a.Name, strconv.FormatInt(a.Uses, 10), output.Unix(a.UsedAt), body,
						})
					}
					p.Table([]string{"id", "shortcut", "uses", "last used", "body"}, rows)
				}
				p.Info("%s shortcuts", output.Quota(list.Quota))
				p.Meta(meta)
				return nil
			})
		},
	}
}

func newActionsViewCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "view <id>",
		Short: "Show one shortcut",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID("action id", args[0])
			if err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			a, meta, err := c.Actions.Get(ctx, id)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(a, func() error {
				printAction(env, a)
				p.Meta(meta)
				return nil
			})
		},
	}
}

func printAction(env *Env, a teal.Action) {
	p := env.Printer()
	p.Details([]output.KV{
		{Key: "ID", Value: strconv.FormatInt(a.ID, 10)},
		{Key: "Name", Value: a.Name},
		{Key: "Uses", Value: strconv.FormatInt(a.Uses, 10)},
		{Key: "Last used", Value: output.Unix(a.UsedAt)},
		{Key: "Created", Value: output.Time(a.CreatedAt)},
		{Key: "Updated", Value: output.Time(a.UpdatedAt)},
	})
	p.Println()
	p.Block(a.Body)
}

// bodyFlags are where a shortcut's body can come from.
type bodyFlags struct {
	body   string
	file   string
	editor bool
}

func (b *bodyFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&b.body, "body", "b", "", "the text the shortcut expands to")
	cmd.Flags().StringVarP(&b.file, "body-file", "F", "", "read the body from a file (- for standard input)")
	cmd.Flags().BoolVarP(&b.editor, "editor", "e", false, "write the body in $EDITOR")
}

func (b *bodyFlags) validate(cmd *cobra.Command) error {
	return exclusive(cmd, "body", "body-file", "editor")
}

func (b *bodyFlags) given(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("body") || b.file != "" || b.editor
}

// read returns the body the flags point at. initial is what an editor opens
// with.
func (b *bodyFlags) read(env *Env, cmd *cobra.Command, initial string) (string, error) {
	var body string
	switch {
	case cmd.Flags().Changed("body"):
		body = b.body
	case b.file == "-":
		raw, err := env.IO.ReadAll(maxBody)
		if err != nil {
			return "", fmt.Errorf("reading the body: %w", err)
		}
		body = string(raw)
	case b.file != "":
		raw, err := readFileCapped(b.file, maxBody)
		if err != nil {
			return "", fmt.Errorf("reading the body: %w", err)
		}
		body = string(raw)
	default:
		var err error
		if body, err = editText(env, initial); err != nil {
			return "", err
		}
	}
	body = strings.TrimRight(body, "\r\n")
	if strings.TrimSpace(body) == "" {
		return "", usageError(errors.New("the body is empty"))
	}
	return body, nil
}

func newActionsCreateCmd(env *Env) *cobra.Command {
	var b bodyFlags
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a shortcut",
		Long: `Create a shortcut. The body comes from --body, --body-file, or — with none of
them, on a terminal — the editor in $VISUAL or $EDITOR.

Under credits and hybrid billing a shortcut beyond the plan's ceiling is
charged once, when it is created.`,
		Example: `  celadon actions create hi --body 'Hi [[YOU_FIRST]]!'
  celadon actions create order --body 'Order [[ARG1]] for [[ARG2]] is on its way'
  celadon actions create sig --editor`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := b.validate(cmd); err != nil {
				return err
			}
			name := strings.TrimSpace(args[0])
			if name == "" {
				return usageError(errors.New("the name is empty"))
			}
			if !b.given(cmd) && !env.IO.CanPrompt() {
				return usageError(errors.New("no body: pass --body or --body-file"))
			}
			body, err := b.read(env, cmd, "")
			if err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			a, meta, err := c.Actions.Create(ctx, teal.ActionCreateRequest{Name: name, Body: body})
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(a, func() error {
				p.Success("Created shortcut %q (#%d)", a.Name, a.ID)
				p.Meta(meta)
				return nil
			})
		},
	}
	b.register(cmd)
	return cmd
}

func newActionsEditCmd(env *Env) *cobra.Command {
	var (
		b    bodyFlags
		name string
	)
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Rename a shortcut or change what it says",
		Long: `Rename a shortcut, change its body, or both. Only what is given is written;
with nothing given, on a terminal, the current body opens in the editor.`,
		Example: `  celadon actions edit 12 --name hello
  celadon actions edit 12 --body-file greeting.txt
  celadon actions edit 12`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := b.validate(cmd); err != nil {
				return err
			}
			id, err := parseID("action id", args[0])
			if err != nil {
				return err
			}
			renaming := cmd.Flags().Changed("name")
			if !renaming && !b.given(cmd) {
				if !env.IO.CanPrompt() {
					return usageError(errors.New("nothing to change: pass --name, --body or --body-file"))
				}
				b.editor = true
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()

			var req teal.ActionUpdateRequest
			if renaming {
				n := strings.TrimSpace(name)
				if n == "" {
					return usageError(errors.New("the name is empty"))
				}
				req.Name = &n
			}
			if b.given(cmd) {
				initial := ""
				if b.editor {
					current, _, err := c.Actions.Get(ctx, id)
					if err != nil {
						return err
					}
					initial = current.Body
				}
				body, err := b.read(env, cmd, initial)
				if err != nil {
					return err
				}
				if !b.editor || body != initial {
					req.Body = &body
				}
			}
			if req.Name == nil && req.Body == nil {
				env.Printer().Info("Nothing changed.")
				return nil
			}
			a, meta, err := c.Actions.Update(ctx, id, req)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(a, func() error {
				p.Success("Updated shortcut %q (#%d)", a.Name, a.ID)
				p.Meta(meta)
				return nil
			})
		},
	}
	b.register(cmd)
	cmd.Flags().StringVarP(&name, "name", "n", "", "the new name")
	return cmd
}

func newActionsDeleteCmd(env *Env) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a shortcut",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID("action id", args[0])
			if err != nil {
				return err
			}
			if err := confirm(env, yes, fmt.Sprintf("Delete shortcut #%d?", id)); err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			meta, err := c.Actions.Delete(ctx, id)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(map[string]any{"ok": true, "id": id}, func() error {
				p.Success("Deleted shortcut #%d", id)
				p.Meta(meta)
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func newActionsPlaceholdersCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "placeholders",
		Short: "List the placeholders a body may carry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			list, meta, err := c.Actions.Placeholders(ctx)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(list, func() error {
				rows := make([][]string, 0, len(list))
				for _, ph := range list {
					open := "yes"
					if !ph.Allowed {
						open = "needs " + output.Or(ph.RequiredPlan)
					}
					rows = append(rows, []string{ph.Token, ph.Level, open, ph.Label})
				}
				p.Table([]string{"placeholder", "level", "open", "renders as"}, rows)
				p.Meta(meta)
				return nil
			})
		},
	}
}

// editText opens the person's editor on initial and returns what they saved.
func editText(env *Env, initial string) (string, error) {
	if !env.IO.CanPrompt() || !env.IO.OutTTY {
		return "", usageError(errors.New("no terminal to open an editor on"))
	}
	editor := firstNonEmpty(getenv("VISUAL"), getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
		if runtime.GOOS == "windows" {
			editor = "notepad"
		}
	}
	f, err := os.CreateTemp("", "celadon-*.txt")
	if err != nil {
		return "", fmt.Errorf("opening the editor: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(initial); err != nil {
		f.Close()
		return "", fmt.Errorf("opening the editor: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("opening the editor: %w", err)
	}

	argv := strings.Fields(editor)
	run := exec.Command(argv[0], append(argv[1:], f.Name())...)
	run.Stdin, run.Stdout, run.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := run.Run(); err != nil {
		return "", fmt.Errorf("the editor (%s) failed: %w", editor, err)
	}
	raw, err := readFileCapped(f.Name(), maxBody)
	if err != nil {
		return "", fmt.Errorf("reading what the editor saved: %w", err)
	}
	return string(raw), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
