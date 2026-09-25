package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/x-chunk/teal"

	"github.com/x-chunk/celadon/internal/output"
)

func newInsightsCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "insights",
		Short:   "Summarize what the archive holds",
		GroupID: groupArchive,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			in, meta, err := c.Insights.Get(ctx)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(in, func() error {
				if len(in.Insights) == 0 {
					p.Info("Nothing to say yet: the archive is empty.")
				}
				for _, s := range in.Insights {
					p.Println("•", output.OneLine(s))
				}
				p.Meta(meta)
				return nil
			})
		},
	}
}

// portraitPoll caps how long one wait for a portrait under construction
// lasts, whatever Retry-After says.
const portraitPoll = 30 * time.Second

func newPortraitCmd(env *Env) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:     "portrait <chat>",
		Short:   "Describe the person on the other side of a chat",
		GroupID: groupArchive,
		Long: `Read a whole conversation and describe the person on the other side: the
archetype they fall into, the traits behind it, what they write about and when.

A portrait that has not been built yet is refused with a time to come back;
--wait keeps asking until it is ready or --timeout runs out. Nothing is charged
for an answer that was not given.

A group's chat id is negative; put -- before it, so it is not read as a flag.`,
		Example: `  celadon portrait 1256738876
  celadon portrait --wait --timeout 5m -- -1001234567890`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chat, err := parseChat(args[0])
			if err != nil {
				return err
			}
			c, _, err := env.Client()
			if err != nil {
				return err
			}
			ctx, cancel := env.Context(cmd, false)
			defer cancel()
			pt, meta, err := fetchPortrait(ctx, env, c, chat, wait)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(pt, func() error {
				printPortrait(env, pt)
				p.Meta(meta)
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "wait for a portrait that is still being built")
	return cmd
}

func fetchPortrait(ctx context.Context, env *Env, c *teal.Client, chat int64, wait bool) (teal.Portrait, *teal.Meta, error) {
	for {
		pt, meta, err := c.Insights.Portrait(ctx, chat)
		e, ok := teal.AsError(err)
		if !ok || e.Code != teal.CodeNotFound || e.RetryAfter <= 0 {
			return pt, meta, err
		}
		if !wait {
			return pt, meta, fmt.Errorf("the portrait is still being built: try again in %s, or pass --wait", output.Duration(e.RetryAfter))
		}
		d := min(e.RetryAfter, portraitPoll)
		env.Printer().Info("The portrait is being built; asking again in %s…", output.Duration(d))
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return pt, meta, ctx.Err()
		case <-t.C:
		}
	}
}

func printPortrait(env *Env, pt teal.Portrait) {
	p := env.Printer()
	subject := output.Or(pt.Subject.Title)
	if pt.Subject.Username != "" {
		subject += " (@" + pt.Subject.Username + ")"
	}
	p.Heading(subject)
	if pt.Summary != "" {
		p.Block(pt.Summary)
		p.Println()
	}
	hours := make([]string, 0, len(pt.Rhythm.PeakHours))
	for _, h := range pt.Rhythm.PeakHours {
		hours = append(hours, fmt.Sprintf("%02d:00", h))
	}
	p.Details([]output.KV{
		{Key: "Archetype", Value: fmt.Sprintf("%s (fit %.0f%%)", output.Or(pt.Archetype.Label), pt.Archetype.Fit*100)},
		{Key: "Confidence", Value: fmt.Sprintf("%s (%.2f)", output.Or(pt.Confidence.Level), pt.Confidence.Score)},
		{Key: "Messages", Value: fmt.Sprintf("%d, %s → %s", pt.Subject.Messages, output.Time(pt.Subject.First), output.Time(pt.Subject.Last))},
		{Key: "Rhythm", Value: strings.Join(nonEmpty(pt.Rhythm.Chronotype, pt.Rhythm.Tempo, pt.Rhythm.Week), " · ")},
		{Key: "Peak hours", Value: output.Or(strings.Join(hours, ", "))},
		{Key: "Per day", Value: fmt.Sprintf("%.1f messages, %.0f%% media", pt.Activity.MessagesPerDay, pt.Activity.MediaShare*100)},
		{Key: "Topics", Value: output.Or(strings.Join(pt.Topics, ", "))},
		{Key: "Built", Value: fmt.Sprintf("%s (model v%d%s)", output.Time(pt.BuiltAt), pt.Model.Version, cachedNote(pt.Cached))},
	})
	if len(pt.Traits) > 0 {
		p.Println()
		rows := make([][]string, 0, len(pt.Traits))
		for _, t := range pt.Traits {
			rows = append(rows, []string{t.Name, t.Level, fmt.Sprintf("%+.2fσ", t.Z)})
		}
		p.Table([]string{"trait", "level", "vs. mean"}, rows)
	}
}

func cachedNote(cached bool) string {
	if cached {
		return ", cached"
	}
	return ""
}

func nonEmpty(values ...string) []string {
	out := values[:0:0]
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return []string{output.Dash}
	}
	return out
}
