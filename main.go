package main

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
)

type options struct {
	output string
	web    bool
	cn     bool
	attr   bool
}

type renderer struct {
	graph       *gen.Graph
	fkCols      map[string]map[string]bool
	includeAttr bool
}

func main() {
	var opts options
	fs := flag.NewFlagSet("enterd", flag.ExitOnError)
	fs.StringVar(&opts.output, "o", "", "write output to file instead of stdout")
	fs.BoolVar(&opts.web, "w", false, "open the generated ERD in Mermaid Live")
	fs.BoolVar(&opts.cn, "cn", false, "use the Mermaid Live China mirror with -w")
	fs.BoolVar(&opts.attr, "attr", false, "include entity fields")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: enterd [flags] <schema-path>\n\n")
		fmt.Fprintf(fs.Output(), "Examples:\n")
		fmt.Fprintf(fs.Output(), "  go run ./cmd/enterd ./internal/ent/schema\n")
		fmt.Fprintf(fs.Output(), "  go run ./cmd/enterd -o docs/erd.mmd ./internal/ent/schema\n")
		fmt.Fprintf(fs.Output(), "  go run ./cmd/enterd -w ./internal/ent/schema\n")
		fmt.Fprintf(fs.Output(), "  go run ./cmd/enterd -w -cn ./internal/ent/schema\n\n")
		fmt.Fprintf(fs.Output(), "Flags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		exit(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}

	graph, err := loadGraph(fs.Arg(0))
	if err != nil {
		exit(fmt.Errorf("load ent schema: %w", err))
	}
	r := newRenderer(graph)
	r.includeAttr = opts.attr
	out := r.render()
	if opts.web {
		if err = openMermaidLive(string(out), opts.cn); err != nil {
			exit(err)
		}
	}
	if opts.output == "" && !opts.web {
		_, err = os.Stdout.Write(out)
	} else if opts.output != "" {
		err = os.MkdirAll(filepath.Dir(opts.output), 0o755)
		if err == nil {
			err = os.WriteFile(opts.output, out, 0o644)
		}
	}
	if err != nil {
		exit(err)
	}
}

func loadGraph(schemaPath string) (_ *gen.Graph, err error) {
	moduleDir, loadPath, ok, err := schemaLoadContext(schemaPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return entc.LoadGraph(schemaPath, &gen.Config{})
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	if err = os.Chdir(moduleDir); err != nil {
		return nil, fmt.Errorf("change to schema module %q: %w", moduleDir, err)
	}
	defer func() {
		if restoreErr := os.Chdir(workingDir); err == nil && restoreErr != nil {
			err = fmt.Errorf("restore working directory %q: %w", workingDir, restoreErr)
		}
	}()
	return entc.LoadGraph(loadPath, &gen.Config{})
}

func schemaLoadContext(schemaPath string) (moduleDir, loadPath string, ok bool, err error) {
	absolutePath, err := filepath.Abs(schemaPath)
	if err != nil {
		return "", "", false, fmt.Errorf("resolve schema path %q: %w", schemaPath, err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		if os.IsNotExist(err) {
			// The argument may be a Go import path rather than a local directory.
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("stat schema path %q: %w", schemaPath, err)
	}
	if !info.IsDir() {
		return "", "", false, fmt.Errorf("schema path %q is not a directory", schemaPath)
	}
	if resolvedPath, resolveErr := filepath.EvalSymlinks(absolutePath); resolveErr == nil {
		absolutePath = resolvedPath
	}
	moduleDir, ok = findModuleDir(absolutePath)
	if !ok {
		return "", "", false, nil
	}
	relativePath, err := filepath.Rel(moduleDir, absolutePath)
	if err != nil {
		return "", "", false, fmt.Errorf("resolve schema path relative to module: %w", err)
	}
	if relativePath == "." {
		loadPath = "."
	} else {
		loadPath = "." + string(filepath.Separator) + relativePath
	}
	return moduleDir, loadPath, true, nil
}

func findModuleDir(dir string) (string, bool) {
	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func exit(err error) {
	fmt.Fprintf(os.Stderr, "enterd: %v\n", err)
	os.Exit(1)
}

func openMermaidLive(code string, cn bool) error {
	url, err := mermaidLiveURL(code, cn)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func mermaidLiveURL(code string, cn bool) (string, error) {
	state := struct {
		Code          string `json:"code"`
		Mermaid       string `json:"mermaid"`
		UpdateDiagram bool   `json:"updateDiagram"`
		Rough         bool   `json:"rough"`
		PanZoom       bool   `json:"panZoom"`
		Grid          bool   `json:"grid"`
	}{
		Code:          code,
		Mermaid:       `{"theme":"default"}`,
		UpdateDiagram: true,
		Rough:         false,
		PanZoom:       true,
		Grid:          true,
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	var compressed bytes.Buffer
	zw, err := zlib.NewWriterLevel(&compressed, zlib.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err = zw.Write(raw); err != nil {
		_ = zw.Close()
		return "", err
	}
	if err = zw.Close(); err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(compressed.Bytes())
	baseURL := "https://mermaid.live"
	if cn {
		baseURL = "https://mermaid-live.nodejs.cn"
	}
	return baseURL + "/edit#pako:" + encoded, nil
}

func newRenderer(graph *gen.Graph) *renderer {
	r := &renderer{
		graph:  graph,
		fkCols: make(map[string]map[string]bool),
	}
	for _, node := range graph.Nodes {
		for _, edge := range node.Edges {
			if !edge.OwnFK() || len(edge.Rel.Columns) == 0 {
				continue
			}
			if fkNode := r.nodeByTable(edge.Rel.Table); fkNode != nil {
				r.markFK(fkNode, edge.Rel.Columns[0])
			}
		}
	}
	return r
}

func (r *renderer) nodeByTable(table string) *gen.Type {
	for _, node := range r.graph.Nodes {
		if node.Table() == table {
			return node
		}
	}
	return nil
}

func (r *renderer) markFK(node *gen.Type, column string) {
	entity := entityName(node)
	if r.fkCols[entity] == nil {
		r.fkCols[entity] = make(map[string]bool)
	}
	r.fkCols[entity][column] = true
}

func (r *renderer) render() []byte {
	var buf bytes.Buffer
	buf.WriteString("erDiagram\n")
	for _, node := range sortedNodes(r.graph.Nodes) {
		r.renderEntity(&buf, node)
	}
	for _, rel := range r.relationships() {
		fmt.Fprintf(&buf, "  %s %s--%s %s : %s\n", rel.left, rel.leftCard, rel.rightCard, rel.right, rel.label)
	}
	return buf.Bytes()
}

func (r *renderer) renderEntity(buf *bytes.Buffer, node *gen.Type) {
	entity := entityName(node)
	if !r.includeAttr {
		fmt.Fprintf(buf, "  %s\n", entity)
		return
	}
	fmt.Fprintf(buf, "  %s {\n", entity)
	if node.ID != nil {
		fmt.Fprintf(buf, "    %s %s PK\n", fieldType(node.ID), fieldName(node.ID.StorageKey()))
	}
	for _, field := range node.Fields {
		markers := r.fieldMarkers(entity, field)
		if markers != "" {
			markers = " " + markers
		}
		fmt.Fprintf(buf, "    %s %s%s\n", fieldType(field), fieldName(field.StorageKey()), markers)
	}
	for _, col := range r.implicitFKColumns(node) {
		fmt.Fprintf(buf, "    int %s FK\n", fieldName(col))
	}
	buf.WriteString("  }\n")
}

func (r *renderer) fieldMarkers(entity string, field *gen.Field) string {
	var markers []string
	if field.Unique {
		markers = append(markers, "UK")
	}
	if r.fkCols[entity][field.StorageKey()] {
		markers = append(markers, "FK")
	}
	return strings.Join(markers, ",")
}

func (r *renderer) implicitFKColumns(node *gen.Type) []string {
	known := make(map[string]bool)
	if node.ID != nil {
		known[node.ID.StorageKey()] = true
	}
	for _, field := range node.Fields {
		known[field.StorageKey()] = true
	}
	var cols []string
	for col := range r.fkCols[entityName(node)] {
		if !known[col] {
			cols = append(cols, col)
		}
	}
	sort.Strings(cols)
	return cols
}

type relationship struct {
	left      string
	leftCard  string
	right     string
	rightCard string
	label     string
}

func (r *renderer) relationships() []relationship {
	var rels []relationship
	seen := make(map[string]bool)
	for _, node := range sortedNodes(r.graph.Nodes) {
		for _, edge := range node.Edges {
			if edge.IsInverse() && edge.Ref != nil {
				continue
			}
			rel, ok := relationshipFor(edge)
			if !ok {
				continue
			}
			key := strings.Join([]string{rel.left, rel.leftCard, rel.right, rel.rightCard, rel.label}, "|")
			if seen[key] {
				continue
			}
			seen[key] = true
			rels = append(rels, rel)
		}
	}
	sort.Slice(rels, func(i, j int) bool {
		if rels[i].left != rels[j].left {
			return rels[i].left < rels[j].left
		}
		if rels[i].right != rels[j].right {
			return rels[i].right < rels[j].right
		}
		return rels[i].label < rels[j].label
	})
	return rels
}

func relationshipFor(edge *gen.Edge) (relationship, bool) {
	left := entityName(edge.Owner)
	right := entityName(edge.Type)
	label := relationLabel(edge.Name)
	switch edge.Rel.Type {
	case gen.O2M:
		return relationship{
			left:      left,
			leftCard:  requiredOne(edge.Ref),
			right:     right,
			rightCard: "o{",
			label:     label,
		}, true
	case gen.M2O:
		return relationship{
			left:      right,
			leftCard:  requiredOne(edge),
			right:     left,
			rightCard: "o{",
			label:     label,
		}, true
	case gen.O2O:
		return relationship{
			left:      left,
			leftCard:  requiredOne(edge.Ref),
			right:     right,
			rightCard: requiredOne(edge),
			label:     label,
		}, true
	case gen.M2M:
		return relationship{
			left:      left,
			leftCard:  "}o",
			right:     right,
			rightCard: "o{",
			label:     label,
		}, true
	default:
		return relationship{}, false
	}
}

func requiredOne(edge *gen.Edge) string {
	if edge == nil || edge.Optional {
		return "o|"
	}
	return "||"
}

func sortedNodes(nodes []*gen.Type) []*gen.Type {
	out := append([]*gen.Type(nil), nodes...)
	sort.Slice(out, func(i, j int) bool {
		return entityName(out[i]) < entityName(out[j])
	})
	return out
}

func entityName(node *gen.Type) string {
	return strings.ToUpper(cleanIdentifier(node.Table()))
}

func fieldName(name string) string {
	return cleanIdentifier(name)
}

func fieldType(field *gen.Field) string {
	if field.Type == nil {
		return "unknown"
	}
	return cleanIdentifier(field.Type.String())
}

func relationLabel(name string) string {
	return cleanIdentifier(name)
}

var identPart = regexp.MustCompile(`[^A-Za-z0-9_]+`)

func cleanIdentifier(s string) string {
	s = identPart.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "unknown"
	}
	if r, _ := utf8FirstRune(s); unicode.IsDigit(r) {
		s = "_" + s
	}
	return s
}

func utf8FirstRune(s string) (rune, int) {
	for i, r := range s {
		return r, i
	}
	return 0, 0
}
