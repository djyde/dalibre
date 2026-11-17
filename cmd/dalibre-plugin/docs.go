package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"dalibre/sdk"
)

// cmdDocs prints the plugin authors' guide: its topics, one topic, or all
// of it, as Markdown.
func cmdDocs(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("docs", flag.ExitOnError)
	all := fs.Bool("all", false, "print the whole guide")
	topics := parseAny(fs, args)
	if *all {
		fmt.Println(sdk.Intro())
		for _, s := range sdk.Guide() {
			if s.Level == 2 {
				fmt.Printf("\n%s\n", s.Text)
			}
		}
		return nil
	}
	if len(topics) == 0 {
		fmt.Printf("Writing Dalibre plugins (plugin API %s). Read a topic with: dalibre-plugin docs <topic>\n", apiVersion())
		fmt.Println("The whole guide: dalibre-plugin docs --all. The types: dalibre-plugin types.")
		fmt.Println()
		for _, s := range sdk.Guide() {
			pad := strings.Repeat("  ", s.Level-2)
			fmt.Printf("  %-32s %s\n", pad+s.Slug, s.Title)
		}
		return nil
	}
	for i, t := range topics {
		found := sdk.FindSections(t)
		switch {
		case len(found) == 0:
			return fmt.Errorf("no topic %q; dalibre-plugin docs lists them", t)
		case len(found) > 1:
			fmt.Fprintf(os.Stderr, "%q is in several topics; pick one:\n", t)
			for _, s := range found {
				fmt.Fprintf(os.Stderr, "  %-30s %s\n", s.Slug, s.Title)
			}
			return errors.New("ambiguous topic")
		}
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(found[0].Text)
	}
	return nil
}

// cmdTypes prints dalibre.d.ts: its declarations, or the ones named.
func cmdTypes(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("types", flag.ExitOnError)
	all := fs.Bool("all", false, "print the whole of dalibre.d.ts")
	names := parseAny(fs, args)
	if *all {
		data, err := sdk.Files.ReadFile("dalibre.d.ts")
		if err != nil {
			return err
		}
		os.Stdout.Write(data)
		return nil
	}
	if len(names) == 0 {
		fmt.Println("The API's types, from dalibre.d.ts. Read one with: dalibre-plugin types <Name>")
		fmt.Println()
		for _, d := range sdk.Types() {
			sum := d.Summary
			if r := []rune(sum); len(r) > 90 {
				sum = string(r[:89]) + "…"
			}
			fmt.Printf("  %-16s %s\n", d.Name, sum)
		}
		return nil
	}
	for i, n := range names {
		d, ok := sdk.FindType(n)
		if !ok {
			return fmt.Errorf("no type %q; dalibre-plugin types lists them", n)
		}
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(d.Text)
	}
	return nil
}

// parseAny parses flags wherever they are among the arguments, and
// returns the others, however many.
func parseAny(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			os.Exit(2)
		}
		if fs.NArg() == 0 {
			return pos
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func apiVersion() string { return fmt.Sprint(pluginAPI) }
