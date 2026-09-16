package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/TAbelhaDev/tabelhascaff/ipc"
)

// cardJSON is the wire format for the ipc subcommand.
type cardJSON struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	Body  string `json:"body,omitempty"`
}

type columnJSON struct {
	Name  string     `json:"name"`
	Path  string     `json:"path"`
	Cards []cardJSON `json:"cards"`
}

type boardJSON struct {
	Name    string       `json:"name"`
	Path    string       `json:"path"`
	Columns []columnJSON `json:"columns"`
}

func boardsToJSON(boards []Board) []boardJSON {
	out := make([]boardJSON, 0, len(boards))
	for _, b := range boards {
		bj := boardJSON{Name: b.Name, Path: b.Path}
		for _, c := range b.Columns {
			cj := columnJSON{Name: c.Name, Path: c.Path}
			for _, card := range c.Cards {
				cj.Cards = append(cj.Cards, cardJSON{Title: card.Title, Path: card.Path, Body: card.Body})
			}
			bj.Columns = append(bj.Columns, cj)
		}
		out = append(out, bj)
	}
	return out
}

// runIPC implements `takanban ipc <método> [key=value...] --json`, the
// same scriptable-data-source convention as dcal/djobs/tradar.
func runIPC(args []string) int {
	parsed, err := ipc.ParseIPCArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "uso: takanban ipc <método> [key=value...] --json")
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	roots, cfgWarning := loadRootsConfig()
	boards, warnings := scanBoards(roots)
	if cfgWarning != "" {
		warnings = append([]string{cfgWarning}, warnings...)
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "aviso:", w)
	}

	switch parsed.Method {
	case "boards.list":
		out := boardsToJSON(boards)
		if name, ok := parsed.Filters["name"]; ok {
			filtered := out[:0]
			for _, b := range out {
				if b.Name == name {
					filtered = append(filtered, b)
				}
			}
			out = filtered
		}
		return ipc.WriteJSON(out)
	case "boards.next":
		return ipcBoardsNext(boards)
	case "cards.create":
		return ipcCardsCreate(boards, parsed.Filters)
	case "cards.update":
		return ipcCardsUpdate(boards, parsed.Filters)
	case "cards.batch":
		return ipcCardsBatch(boards, parsed.Filters)
	case "cards.move":
		return ipcCardsMove(boards, parsed.Filters)
	default:
		fmt.Fprintf(os.Stderr, "método desconhecido: %q\n", parsed.Method)
		return 1
	}
}

// findBoard/column resolve a board and column from ipc filters. Returns an
// error string when the board/column doesn't exist.
func findColumn(boards []Board, boardName, columnName string) (*Board, *Column, string) {
	if boardName == "" {
		return nil, nil, "filtro board= é obrigatório"
	}
	for i := range boards {
		if boards[i].Name != boardName {
			continue
		}
		for j := range boards[i].Columns {
			if boards[i].Columns[j].Name == columnName {
				return &boards[i], &boards[i].Columns[j], ""
			}
		}
		return nil, nil, fmt.Sprintf("coluna %q não existe no board %q", columnName, boardName)
	}
	return nil, nil, fmt.Sprintf("board %q não existe", boardName)
}

// ipcCardsCreate creates a card in a board/column and prints it as JSON.
// Filters: board=, column=, title=.
func ipcCardsCreate(boards []Board, filters map[string]string) int {
	_, col, ferr := findColumn(boards, filters["board"], filters["column"])
	if ferr != "" {
		fmt.Fprintln(os.Stderr, "erro:", ferr)
		return 1
	}
	card, err := createCard(*col, filters["title"])
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		return 1
	}
	return ipc.WriteJSON(cardJSON{Title: card.Title, Path: card.Path, Body: card.Body})
}

// ipcCardsUpdate replaces a card's body, keeping its due-date front matter.
// Filters: board=, column=, title=, body=.
func ipcCardsUpdate(boards []Board, filters map[string]string) int {
	_, col, ferr := findColumn(boards, filters["board"], filters["column"])
	if ferr != "" {
		fmt.Fprintln(os.Stderr, "erro:", ferr)
		return 1
	}
	title := filters["title"]
	for i := range col.Cards {
		if col.Cards[i].Title == title {
			updated, err := updateCardBody(col.Cards[i], filters["body"])
			if err != nil {
				fmt.Fprintln(os.Stderr, "erro:", err)
				return 1
			}
			return ipc.WriteJSON(cardJSON{Title: updated.Title, Path: updated.Path, Body: updated.Body})
		}
	}
	fmt.Fprintf(os.Stderr, "erro: card %q não existe na coluna %q do board %q\n", title, col.Name, filters["board"])
	return 1
}

// ipcCardsMove moves a card to another column and prints it as JSON.
// Filters: board=, title=, from= (column), to= (column).
func ipcCardsMove(boards []Board, filters map[string]string) int {
	b, from, ferr := findColumn(boards, filters["board"], filters["from"])
	if ferr != "" {
		fmt.Fprintln(os.Stderr, "erro:", ferr)
		return 1
	}
	toName := filters["to"]
	if toName == "" {
		fmt.Fprintln(os.Stderr, "erro: filtro to= é obrigatório")
		return 1
	}
	_, to, terr := findColumn([]Board{*b}, b.Name, toName)
	if terr != "" {
		fmt.Fprintln(os.Stderr, "erro:", terr)
		return 1
	}

	title := filters["title"]
	for i := range from.Cards {
		if from.Cards[i].Title == title {
			moved, err := moveCard(from.Cards[i], *to)
			if err != nil {
				fmt.Fprintln(os.Stderr, "erro:", err)
				return 1
			}
			return ipc.WriteJSON(cardJSON{Title: moved.Title, Path: moved.Path, Body: moved.Body})
		}
	}
	fmt.Fprintf(os.Stderr, "erro: card %q não existe na coluna %q do board %q\n", title, from.Name, b.Name)
	return 1
}

// batchCardItem is the wire format for a single card in cards.batch.
// Accepts both body and placeholder (the suggestion contract uses placeholder).
type batchCardItem struct {
	Title       string `json:"title"`
	Body        string `json:"body,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

// ipcCardsBatch creates multiple cards idempotently — cards whose title
// already exists in the column are silently skipped (no " (2)" suffix).
// Filters: board=, column=, cards= (JSON array of {title, body}).
func ipcCardsBatch(boards []Board, filters map[string]string) int {
	_, col, ferr := findColumn(boards, filters["board"], filters["column"])
	if ferr != "" {
		fmt.Fprintln(os.Stderr, "erro:", ferr)
		return 1
	}
	rawCards := filters["cards"]
	if rawCards == "" {
		fmt.Fprintln(os.Stderr, "erro: filtro cards= é obrigatório (array JSON)")
		return 1
	}
	var items []batchCardItem
	if err := json.Unmarshal([]byte(rawCards), &items); err != nil {
		fmt.Fprintf(os.Stderr, "erro ao interpretar cards=: %v\n", err)
		return 1
	}

	// Build a set of existing titles for O(1) lookup.
	existing := make(map[string]bool, len(col.Cards))
	for _, c := range col.Cards {
		existing[c.Title] = true
	}

	var created []cardJSON
	for _, item := range items {
		if item.Title == "" || existing[item.Title] {
			continue
		}
		body := item.Body
		if body == "" {
			body = item.Placeholder
		}
		card, err := createCardBody(*col, item.Title, body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "erro ao criar card %q: %v\n", item.Title, err)
			continue
		}
		created = append(created, cardJSON{Title: card.Title, Path: card.Path, Body: card.Body})
		existing[card.Title] = true
	}
	return ipc.WriteJSON(created)
}

// ipcBoardsNext returns the single card tabelhakanban itself would put first:
// the top card of the first column that isn't a "done"-ish column (by name),
// falling back to the first card of the first column if every column looks
// done.
func ipcBoardsNext(boards []Board) int {
	if len(boards) == 0 {
		return ipc.WriteJSON(nil)
	}
	b := boards[0]
	for _, c := range b.Columns {
		if isDoneColumn(c.Name) || len(c.Cards) == 0 {
			continue
		}
		return ipc.WriteJSON(boardJSON{Name: b.Name, Path: b.Path, Columns: []columnJSON{{
			Name: c.Name, Path: c.Path, Cards: []cardJSON{{Title: c.Cards[0].Title, Path: c.Cards[0].Path, Body: c.Cards[0].Body}},
		}}})
	}
	for _, c := range b.Columns {
		if len(c.Cards) == 0 {
			continue
		}
		return ipc.WriteJSON(boardJSON{Name: b.Name, Path: b.Path, Columns: []columnJSON{{
			Name: c.Name, Path: c.Path, Cards: []cardJSON{{Title: c.Cards[0].Title, Path: c.Cards[0].Path, Body: c.Cards[0].Body}},
		}}})
	}
	return ipc.WriteJSON(nil)
}

// isDoneColumn matches a column name against the configured markers
// ([ipc].done_column_markers). Only boards.next uses this — the TUI itself
// has no notion of a "done" column.
func isDoneColumn(name string) bool {
	for _, marker := range settings.IPC.DoneColumnMarkers {
		if strings.Contains(strings.ToLower(name), strings.ToLower(marker)) {
			return true
		}
	}
	return false
}
