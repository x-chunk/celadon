package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.xchunk.org/teal"

	"github.com/x-chunk/celadon/internal/api"
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
			r, err := c.Insights.Get(ctx).Value()
			if err != nil {
				return err
			}
			in, meta := r.Data, r.Meta
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

// portraitPoll caps how long one wait for a portrait whose model is still
// being fitted lasts, whatever the API asks for; portraitPause is how long
// one lasts when the API does not say.
const (
	portraitPoll  = 30 * time.Second
	portraitPause = 5 * time.Second
)

func newPortraitCmd(env *Env) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:     "portrait <chat>",
		Short:   "Describe the person on the other side of a chat",
		GroupID: groupArchive,
		Long: `Read a whole conversation and describe the person on the other side: the
archetype they fall into, the traits behind it, what they write about and when,
which of the account's other conversations sound like it, and what is worth
knowing about the analysis — a style that shifted, say. Whether the last two
are given depends on the plan.

A portrait whose model is still being fitted is refused with a time to come
back; --wait keeps asking until it is ready or --timeout runs out. A chat too
short to portray, or one whose other side never wrote, is refused until it
grows, and a deployment with portraits switched off refuses them all. Nothing
is charged for an answer that was not given.

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
			r, err := fetchPortrait(ctx, env, c, chat, wait)
			if err != nil {
				return err
			}
			p := env.Printer()
			return p.Result(r.Data, func() error {
				printPortrait(env, r.Data)
				p.Meta(r.Meta)
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "wait for a portrait whose model is still being fitted")
	return cmd
}

func fetchPortrait(ctx context.Context, env *Env, c *teal.Client, chat int64, wait bool) (teal.Response[teal.Portrait], error) {
	for {
		r, err := c.Insights.Portrait(ctx, chat).Value()
		after, training := api.PortraitTraining(err)
		if !training {
			return r, err
		}
		if !wait {
			when := "shortly"
			if after > 0 {
				when = "in " + output.Duration(after)
			}
			return r, fmt.Errorf("the portrait's model is still being fitted: try again %s, or pass --wait", when)
		}
		d := portraitPause
		if after > 0 {
			d = min(after, portraitPoll)
		}
		env.Printer().Info("The portrait's model is being fitted; asking again in %s…", output.Duration(d))
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return r, ctx.Err()
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
	pairs := []output.KV{
		{Key: "Archetype", Value: fmt.Sprintf("%s (fit %.0f%%)", output.Or(pt.Archetype.Label), pt.Archetype.Fit*100)},
	}
	if pt.Archetype.Description != "" {
		pairs = append(pairs, output.KV{Key: "Which is", Value: pt.Archetype.Description})
	}
	if len(pt.Archetype.Terms) > 0 {
		// The archetype's own words, not this subject's.
		pairs = append(pairs, output.KV{Key: "Its words", Value: strings.Join(pt.Archetype.Terms, ", ")})
	}
	p.Details(append(pairs,
		output.KV{Key: "Confidence", Value: fmt.Sprintf("%s (%.2f)", output.Or(pt.Confidence.Level), pt.Confidence.Score)},
		output.KV{Key: "Messages", Value: fmt.Sprintf("%d, %s → %s", pt.Subject.Messages, output.Time(pt.Subject.First), output.Time(pt.Subject.Last))},
		output.KV{Key: "They wrote", Value: activityText(pt.Activity)},
		output.KV{Key: "Rhythm", Value: strings.Join(nonEmpty(pt.Rhythm.Chronotype, pt.Rhythm.Tempo, pt.Rhythm.Week), " · ")},
		output.KV{Key: "Peak hours", Value: output.Or(strings.Join(hours, ", "))},
		output.KV{Key: "Per day", Value: fmt.Sprintf("%.1f messages, %.0f%% media", pt.Activity.MessagesPerDay, pt.Activity.MediaShare*100)},
		output.KV{Key: "Topics", Value: output.Or(strings.Join(pt.Topics, ", "))},
		output.KV{Key: "Built", Value: fmt.Sprintf("%s (model v%d%s)", output.Time(pt.BuiltAt), pt.Model.Version, cachedNote(pt.Cached))},
	))
	if len(pt.Traits) > 0 {
		p.Println()
		rows := make([][]string, 0, len(pt.Traits))
		for _, t := range pt.Traits {
			rows = append(rows, []string{t.Name, t.Level, fmt.Sprintf("%+.2fσ", t.Z), t.Note})
		}
		p.Table([]string{"trait", "level", "vs. mean", "note"}, rows)
	}
	if len(pt.Similar) > 0 {
		p.Println()
		rows := make([][]string, 0, len(pt.Similar))
		for _, s := range pt.Similar {
			rows = append(rows, []string{output.Or(s.Title), fmt.Sprintf("%.2f", s.Score)})
		}
		p.Table([]string{"sounds like", "score"}, rows)
	}
	if len(pt.Signals) > 0 {
		p.Println()
		p.Heading("Worth knowing")
		for _, sig := range pt.Signals {
			line := "• " + output.OneLine(sig.Text)
			if sig.Severity != "" && sig.Severity != "info" {
				line += " (" + output.OneLine(sig.Severity) + ")"
			}
			p.Println(line)
		}
	}
}

// activityText is how much a subject wrote, and over what span.
func activityText(a teal.PortraitActivity) string {
	s := fmt.Sprintf("%d messages, %d words, %d distinct", a.Messages, a.Words, a.UniqueTerms)
	if !a.First.IsZero() || !a.Last.IsZero() {
		s += ", " + output.Time(a.First) + " → " + output.Time(a.Last)
	}
	return s
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
