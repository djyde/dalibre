package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"dalibre/internal/plugin"
)

// Plugins' actions, for an agent: what the user runs from the book menu
// and the sidebar's Tools, run without the window. The forms an action
// asks are answered by the agent, ahead; what it wants changed is
// returned, and written only when the agent says to.

// ActionInfo is an action a loaded plugin has.
type ActionInfo struct {
	Plugin     string   `json:"plugin" jsonschema:"the plugin's id"`
	PluginName string   `json:"plugin_name"`
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Scope      string   `json:"scope" jsonschema:"books: runs on the books given. library: runs on every book"`
	MinBooks   int      `json:"min_books,omitempty"`
	MaxBooks   int      `json:"max_books,omitempty"`
	Formats    []string `json:"formats,omitempty" jsonschema:"every book must have one of these formats"`
	// ChangesBooks is whether the user let the plugin change books
	// without asking.
	ChangesBooks bool `json:"changes_books,omitempty" jsonschema:"the user lets this plugin change books' details"`
}

// ActionBook is a book an action found.
type ActionBook struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Note  string `json:"note,omitempty" jsonschema:"what the action says of the book"`
}

// ActionGroup is books an action found, under a title.
type ActionGroup struct {
	Title string       `json:"title"`
	Books []ActionBook `json:"books"`
}

// FieldChange is one detail of a book an action changes.
type FieldChange struct {
	Field string `json:"field" jsonschema:"title, authors, series, tags, publisher, pubdate, languages, identifiers, rating or comments"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// BookChanges are the changes an action asks for in one book.
type BookChanges struct {
	Book    BookRef       `json:"book"`
	Changes []FieldChange `json:"changes"`
}

// NewFile is a file an action made.
type NewFile struct {
	Book   *BookRef `json:"book,omitempty" jsonschema:"the book the file is a format of; none for a new book"`
	Name   string   `json:"name"`
	Format string   `json:"format"`
}

// ActionOutcome is what an action returned, and what was done with it.
type ActionOutcome struct {
	Message string        `json:"message,omitempty" jsonschema:"how the action says it went"`
	Groups  []ActionGroup `json:"groups,omitempty" jsonschema:"the books it found"`
	Changes []BookChanges `json:"changes,omitempty" jsonschema:"the changes to books' details it asks for"`
	Files   []NewFile     `json:"files,omitempty" jsonschema:"the files it made: new books, and new formats of books"`
	Open    string        `json:"open,omitempty" jsonschema:"a page it asks to open"`
	Applied bool          `json:"applied" jsonschema:"whether the changes and files were written into the library"`
	Changed []BookRef     `json:"changed,omitempty" jsonschema:"applied: the books changed"`
	Added   []BookRef     `json:"added,omitempty" jsonschema:"applied: the books added"`
	Failed  []string      `json:"failed,omitempty" jsonschema:"applied: what couldn't be written"`
}

// ActionRun is an action to run.
type ActionRun struct {
	Plugin  string         `json:"plugin" jsonschema:"the plugin's id, from list_actions"`
	Action  string         `json:"action" jsonschema:"the action's id, from list_actions"`
	Books   []int64        `json:"books,omitempty" jsonschema:"for a books action: the ids of the books to run it on, in order"`
	Answers map[string]any `json:"answers,omitempty" jsonschema:"answers to the form the action asks, by field key; run it without them to be told the fields"`
	Apply   bool           `json:"apply,omitempty" jsonschema:"write the changes and files it returns into the library; without it nothing changes and they are only returned"`
}

// Actions is what a Host whose plugins have actions gives too.
type Actions interface {
	// Actions are the loaded plugins' actions.
	Actions(ctx context.Context) []ActionInfo
	// RunAction runs an action without the window. An error that isn't a
	// *plugin.Error is about run.
	RunAction(ctx context.Context, run ActionRun) (*ActionOutcome, error)
}

func (s *Server) actions() (Actions, error) {
	if !s.allowsChanges() {
		return nil, errReadOnly
	}
	a, ok := s.host.(Actions)
	if !ok {
		return nil, errors.New("actions need Dalibre running")
	}
	return a, nil
}

func (s *Server) addActionTools() {
	sdk.AddTool(s.srv, readTool("list_actions", "Lists the actions the user's plugins add: what runs on books (merge, clean up) and on the whole library (find duplicates, check quality)."), s.listActions)
	sdk.AddTool(s.srv, &sdk.Tool{Name: "run_action", Description: "Runs a plugin's action from list_actions and returns what it found and what it wants changed. " +
		"Nothing is written unless apply is true. An action that asks a form fails with the fields it needs: give them in answers.",
		Annotations: &sdk.ToolAnnotations{DestructiveHint: boolp(false), OpenWorldHint: boolp(true)}}, s.runAction)
}

type actionsOut struct {
	Actions []ActionInfo `json:"actions"`
}

func (s *Server) listActions(ctx context.Context, _ *sdk.CallToolRequest, _ noIn) (*sdk.CallToolResult, actionsOut, error) {
	a, err := s.actions()
	if err != nil {
		return nil, actionsOut{}, err
	}
	out := actionsOut{Actions: a.Actions(ctx)}
	if out.Actions == nil {
		out.Actions = []ActionInfo{}
	}
	return nil, out, nil
}

type runActionOut struct {
	OK      bool           `json:"ok"`
	Outcome *ActionOutcome `json:"outcome,omitempty"`
	Error   *TestError     `json:"error,omitempty" jsonschema:"why the action failed; kind invalid with the fields it asks for when answers are missing"`
}

func (s *Server) runAction(ctx context.Context, _ *sdk.CallToolRequest, in ActionRun) (*sdk.CallToolResult, runActionOut, error) {
	a, err := s.actions()
	if err != nil {
		return nil, runActionOut{}, err
	}
	out, err := a.RunAction(ctx, in)
	if err != nil {
		var pe *plugin.Error
		if !errors.As(err, &pe) {
			return nil, runActionOut{}, err // the arguments, not the plugin
		}
		return nil, runActionOut{Error: &TestError{Kind: string(pe.Kind), Message: pe.Error(), Stack: pe.Stack}}, nil
	}
	return nil, runActionOut{OK: true, Outcome: out}, nil
}
