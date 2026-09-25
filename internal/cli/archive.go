package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/output"
)

const queryHelp = `Words given as arguments search the text for that phrase. Filters narrow it
further, and are combined left to right in the order they are typed:

  --where field=value    the field equals the value        (AND)
  --where field~value    the field contains the value      (AND)
  --or    field~value    the same, joined with OR

` + "`celadon fields`" + ` lists what a filter may name and how it may match. How many
filters one query may carry, and whether a field may be matched on a substring,
depend on the plan.`

func newSearchCmd(env *Env) *cobra.Command {
	var (
		q    queryFlags
		page int
	)
	cmd := &cobra.Command{
		Use:     "search [words...]",
		Short:   "Search the archived messages",
		GroupID: groupArchive,
		Long: "Search the archive and print one page of matches.\n\n" + queryHelp + `

Every call is one query and is billed as one, and so is every page: under
shared limits each spends one of the plan's daily searches.`,
		Example: `  celadon search invoice
  celadon search --where suser=ann --where 'created=2026-09-01'
  celadon search refund --chat -1001234567890 --page 2
  celadon search --where media=photo --or media=video -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if page < 1 {
				return usageError(errors.New("--page starts at 1"))
			}
			req, err := q.build(args, page-1)
			if err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			res, meta, err := c.Archive.Search(ctx, req)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(res, func() error {
				if len(res.Messages) == 0 {
					p.Info("No messages match.")
				} else {
					p.Table([]string{"id", "chat", "from", "sent", "text"}, messageRows(env, res.Messages))
					p.Info("page %d of %d · %d matches", res.Page+1, max(res.Pages, 1), res.Total)
				}
				p.Meta(meta)
				return nil
			})
		},
	}
	q.register(cmd.Flags())
	cmd.Flags().IntVar(&page, "page", 1, "which page of matches to show")
	return cmd
}

// messageRows lays messages out for a table, fitting the text into what the
// terminal has left once the other columns are drawn.
func messageRows(env *Env, msgs []teal.Message) [][]string {
	rows := make([][]string, 0, len(msgs))
	textWidth := 0
	if w := env.IO.Width(); w > 0 {
		textWidth = max(w-64, 20)
	}
	for _, m := range msgs {
		text := output.OneLine(m.Text)
		if text == "" && m.MediaType != "" {
			text = "[" + m.MediaType + "]"
		}
		if m.Deleted {
			text = "(deleted) " + text
		}
		if m.Versions > 1 {
			text += fmt.Sprintf(" (edited ×%d)", m.Versions-1)
		}
		if textWidth > 0 {
			text = output.Truncate(text, textWidth)
		}
		rows = append(rows, []string{
			strconv.FormatInt(m.ID, 10),
			strconv.FormatInt(m.Chat, 10),
			sender(m),
			output.Time(m.CreatedAt),
			text,
		})
	}
	return rows
}

func sender(m teal.Message) string {
	if m.SenderUsername != "" {
		return "@" + m.SenderUsername
	}
	return strconv.FormatInt(m.Sender, 10)
}

func newCountCmd(env *Env) *cobra.Command {
	var q queryFlags
	cmd := &cobra.Command{
		Use:     "count [words...]",
		Short:   "Count the archived messages a query matches",
		GroupID: groupArchive,
		Long: "Count the messages a query matches, without reading them.\n\n" + queryHelp + `

A count spends no search from the plan's daily allowance. Under credits it is
still billed as one query.`,
		Example: `  celadon count --where media=photo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := q.build(args, 0)
			if err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			res, meta, err := c.Archive.Count(ctx, req)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(res, func() error {
				p.Println(res.Total)
				p.Meta(meta)
				return nil
			})
		},
	}
	q.register(cmd.Flags())
	return cmd
}

func newExportCmd(env *Env) *cobra.Command {
	var (
		q     queryFlags
		file  string
		force bool
	)
	cmd := &cobra.Command{
		Use:     "export [words...]",
		Short:   "Export the matching messages as one JSON document",
		GroupID: groupArchive,
		Long: "Export every message a query matches as one JSON document.\n\n" + queryHelp + `

The document is written to --file, to standard output when that is not a
terminal, and otherwise to aether-export-<time>.json in the current directory.
A file is written beside its destination and moved into place when complete,
so an interrupted export never leaves half a file under the name asked for.

The whole export is priced and charged before the first byte arrives. An
export is capped at 50 MB; one that reaches the cap ends early and its charge
is refunded, and celadon says so.`,
		Example: `  celadon export --chat -1001234567890 -f ann.json
  celadon export --where created~2026-09 > september.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := q.build(args, 0)
			if err != nil {
				return err
			}
			req.Page = 0

			toStdout := file == "-" || (file == "" && !env.IO.OutTTY)
			if file == "" && !toStdout {
				file = "aether-export-" + env.now().Format("20060102-150405") + ".json"
			}
			if !toStdout && !force {
				if _, err := os.Stat(file); err == nil {
					return usageError(fmt.Errorf("%s already exists: pass --force to replace it", file))
				}
			}

			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, true)
			defer cancel()
			body, meta, err := c.Archive.Export(ctx, req)
			if err != nil {
				return err
			}
			defer body.Close()

			total := meta.Header.Get("X-Aether-Export-Total")
			p := env.Printer()
			if toStdout {
				if _, err := io.Copy(env.IO.Out, body); err != nil {
					return fmt.Errorf("streaming the export: %w", err)
				}
				p.Meta(meta)
				return nil
			}

			n, complete, err := writeExport(file, body)
			if err != nil {
				return err
			}
			if !complete {
				p.Warn("the export reached the server's 50 MB cap and ended early; its charge is refunded. Narrow the query and export the rest separately.")
			}
			p.Success("Exported %s messages to %s (%s)", output.Or(total), file, humanBytes(n))
			p.Meta(meta)
			if !complete {
				return silentError{code: ExitError}
			}
			return nil
		},
	}
	q.register(cmd.Flags())
	cmd.Flags().StringVarP(&file, "file", "f", "", "where to write the document (- for standard output)")
	cmd.Flags().BoolVar(&force, "force", false, "replace the file if it exists")
	return cmd
}

// writeExport streams body into a temporary file beside path and moves it
// into place. The temporary file is created 0600, and the archive it holds
// stays readable by its owner alone. It reports whether the document is whole: a stream cut at the
// server's cap is not valid JSON, and is still kept, because what did arrive
// was paid for and may be what somebody needs.
func writeExport(path string, body io.Reader) (n int64, complete bool, err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.part")
	if err != nil {
		return 0, false, fmt.Errorf("creating the export file: %w", err)
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if n, err = io.Copy(tmp, body); err != nil {
		return n, false, fmt.Errorf("streaming the export: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return n, false, fmt.Errorf("writing the export: %w", err)
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		return n, false, fmt.Errorf("checking the export: %w", err)
	}
	complete = validJSON(tmp)
	if err = tmp.Close(); err != nil {
		return n, false, fmt.Errorf("writing the export: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return n, false, fmt.Errorf("moving the export into place: %w", err)
	}
	return n, complete, nil
}

// validJSON reports whether r holds exactly one JSON value, reading it token
// by token so a 50 MB document is not held in memory to find out.
func validJSON(r io.Reader) bool {
	dec := json.NewDecoder(r)
	depth := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return depth == 0
		}
		if err != nil {
			return false
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func newChatsCmd(env *Env) *cobra.Command {
	var page int
	cmd := &cobra.Command{
		Use:     "chats",
		Short:   "List the conversations in the archive",
		GroupID: groupArchive,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if page < 1 {
				return usageError(errors.New("--page starts at 1"))
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			list, meta, err := c.Archive.Chats(ctx, &teal.ChatsRequest{Page: page - 1})
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(list, func() error {
				if len(list.Chats) == 0 {
					p.Info("No chats on this page.")
				} else {
					rows := make([][]string, 0, len(list.Chats))
					for _, ch := range list.Chats {
						rows = append(rows, []string{strconv.FormatInt(ch.ID, 10), output.Or(ch.Title), strconv.FormatInt(ch.Messages, 10)})
					}
					p.Table([]string{"id", "title", "messages"}, rows)
					p.Info("page %d · %d chats in all", list.Page+1, list.Total)
				}
				p.Meta(meta)
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&page, "page", 1, "which page of chats to show")
	return cmd
}

func newMessageCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "message",
		Aliases: []string{"msg"},
		Short:   "Read one archived message and its edit history",
		GroupID: groupArchive,
	}
	cmd.AddCommand(newMessageViewCmd(env), newMessageVersionsCmd(env))
	return cmd
}

func newMessageViewCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "view <id>",
		Short: "Show one message by its archive id",
		Long: `Show one message by the archive's own id — the ID column of a search. It is
not Telegram's message id, which is only unique inside a chat.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID("message id", args[0])
			if err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			m, meta, err := c.Archive.Message(ctx, id)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(m, func() error {
				pairs := []output.KV{
					{Key: "ID", Value: strconv.FormatInt(m.ID, 10)},
					{Key: "Telegram id", Value: strconv.FormatInt(m.MessageID, 10)},
					{Key: "Chat", Value: strconv.FormatInt(m.Chat, 10)},
					{Key: "From", Value: sender(m)},
					{Key: "To", Value: strconv.FormatInt(m.Receiver, 10)},
					{Key: "Sent", Value: output.Time(m.CreatedAt)},
				}
				if m.MediaType != "" {
					media := m.MediaType
					if m.FileID != "" {
						media += " · file " + m.FileID
					}
					pairs = append(pairs, output.KV{Key: "Media", Value: media})
				}
				if m.Deleted {
					pairs = append(pairs, output.KV{Key: "Deleted", Value: "yes — deleted in Telegram, kept in the archive"})
				}
				p.Details(pairs)
				if m.Text != "" {
					p.Println()
					p.Block(m.Text)
				}
				p.Meta(meta)
				return nil
			})
		},
	}
}

func newMessageVersionsCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "versions <id>",
		Short: "Show every text a message has carried, latest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID("message id", args[0])
			if err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			vl, meta, err := c.Archive.Versions(ctx, id)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(vl, func() error {
				for i, v := range vl.Versions {
					if i > 0 {
						p.Println()
					}
					label := output.Unix(v.At)
					if v.Current {
						label += " (current)"
					}
					p.Heading(label)
					p.Block(output.Or(v.Text))
				}
				p.Meta(meta)
				return nil
			})
		},
	}
}

func newFieldsCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "fields",
		Short:   "List the fields a search filter may name",
		GroupID: groupArchive,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			fields, meta, err := c.Archive.Fields(ctx)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(fields, func() error {
				rows := make([][]string, 0, len(fields))
				for _, f := range fields {
					rows = append(rows, []string{f.Key, f.Kind, modeSyntax(f.Key, f.Modes)})
				}
				p.Table([]string{"field", "kind", "write it as"}, rows)
				p.Meta(meta)
				return nil
			})
		},
	}
}

func modeSyntax(field string, modes []string) string {
	parts := make([]string, 0, len(modes))
	for _, m := range modes {
		switch m {
		case teal.MatchEquals:
			parts = append(parts, field+"=…")
		case teal.MatchContains:
			parts = append(parts, field+"~…")
		default:
			parts = append(parts, m)
		}
	}
	return strings.Join(parts, "  ")
}
