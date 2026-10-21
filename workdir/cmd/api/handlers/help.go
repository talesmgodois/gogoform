package handlers

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// helpFS holds the system manual: one markdown file per page, at the path of
// the page's URL. /app/account is documented by help/app/account.md, /app by
// help/app/index.md and /app/forms/{id} by help/app/forms/_id.md (a "_"
// prefix stands for any path segment). help/index.md is the manual's
// overview and the fallback of the pages without a doc of their own. The
// "all:" prefix embeds the "_" files too, which go:embed skips otherwise.
//
//go:embed all:help
var helpFS embed.FS

// helpAnchorPrefix prefixes the id of every heading of a rendered doc, so the
// docs shown in the help sidenav cannot clash with the ids of the page.
// helpIcon links to the heading "## Change email" as "change-email".
const helpAnchorPrefix = "help-"

// helpIndexKey is the key of the manual's overview, the fallback doc.
const helpIndexKey = "index"

// helpDoc is a rendered page of the manual.
type helpDoc struct {
	// Key is the path of the markdown file under help/, without ".md", e.g.
	// "app/forms/_id".
	Key string
	// Title is the text of the first level-1 heading.
	Title string
	// Section groups the doc in the manual's table of contents; Order sorts
	// it within the manual. Both are set by the front matter of the file.
	Section string
	Order   int
	// HTML is the rendered markdown; raw HTML in the source is dropped.
	HTML template.HTML
	// Anchors are the heading ids, without helpAnchorPrefix.
	Anchors []string
}

// HasAnchor tells whether the doc has a heading with the id anchor.
func (d *helpDoc) HasAnchor(anchor string) bool {
	for _, a := range d.Anchors {
		if a == anchor {
			return true
		}
	}
	return false
}

// helpLibrary is the manual, rendered once at startup.
type helpLibrary struct {
	docs map[string]*helpDoc
	// params lists, per directory key ("" for the root), the "_param"
	// entries matching any path segment, files without ".md".
	params map[string][]string
}

// helpDocs is the manual served by the help routes.
var helpDocs = mustLoadHelp(helpFS)

// mustLoadHelp renders every markdown file of the help directory of fsys; it
// panics on a malformed doc, like template.Must, so a broken manual fails at
// startup and in the tests.
func mustLoadHelp(fsys fs.FS) *helpLibrary {
	lib, err := loadHelp(fsys)
	if err != nil {
		panic(err)
	}
	return lib
}

func loadHelp(fsys fs.FS) (*helpLibrary, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID(), parser.WithAttribute()),
	)
	lib := &helpLibrary{docs: map[string]*helpDoc{}, params: map[string][]string{}}
	err := fs.WalkDir(fsys, "help", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "help" {
			return err
		}
		rel := strings.TrimPrefix(p, "help/")
		dir, name := path.Split(rel)
		dir = strings.TrimSuffix(dir, "/")
		if d.IsDir() {
			if strings.HasPrefix(name, "_") {
				lib.params[dir] = append(lib.params[dir], name)
			}
			return nil
		}
		if !strings.HasSuffix(name, ".md") {
			return nil
		}
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		doc, err := renderHelpDoc(md, strings.TrimSuffix(rel, ".md"), src)
		if err != nil {
			return fmt.Errorf("help: %s: %w", p, err)
		}
		lib.docs[doc.Key] = doc
		if base := strings.TrimSuffix(name, ".md"); strings.HasPrefix(base, "_") {
			lib.params[dir] = append(lib.params[dir], base)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if lib.docs[helpIndexKey] == nil {
		return nil, fmt.Errorf("help: missing help/%s.md", helpIndexKey)
	}
	return lib, nil
}

// renderHelpDoc renders src, the markdown of the doc key, after its front
// matter: an optional block of "name: value" lines between "---" lines,
// setting the section and order of the doc.
func renderHelpDoc(md goldmark.Markdown, key string, src []byte) (*helpDoc, error) {
	doc := &helpDoc{Key: key, Section: "Manual"}
	src, err := parseHelpFrontMatter(src, doc)
	if err != nil {
		return nil, err
	}
	root := md.Parser().Parse(text.NewReader(src))
	err = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		if h.Level == 1 && doc.Title == "" {
			doc.Title = helpNodeText(h, src)
		}
		if id, ok := h.AttributeString("id"); ok {
			if b, ok := id.([]byte); ok {
				doc.Anchors = append(doc.Anchors, string(b))
				h.SetAttributeString("id", []byte(helpAnchorPrefix+string(b)))
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	if doc.Title == "" {
		return nil, fmt.Errorf("no level-1 heading")
	}
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, root); err != nil {
		return nil, err
	}
	// The renderer escapes text and drops raw HTML and unsafe link schemes
	// (html.WithUnsafe is not set), so the output is safe to embed.
	doc.HTML = template.HTML(buf.String())
	return doc, nil
}

// parseHelpFrontMatter sets the section and order of doc from the front
// matter of src and returns src without it.
func parseHelpFrontMatter(src []byte, doc *helpDoc) ([]byte, error) {
	s := string(src)
	if !strings.HasPrefix(s, "---\n") {
		return src, nil
	}
	head, body, ok := strings.Cut(s[len("---\n"):], "\n---\n")
	if !ok {
		return nil, fmt.Errorf("unterminated front matter")
	}
	for _, line := range strings.Split(head, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(name) {
		case "section":
			doc.Section = value
		case "order":
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("order: %w", err)
			}
			doc.Order = n
		}
	}
	return []byte(body), nil
}

// helpNodeText returns the text of n and its descendants.
func helpNodeText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			b.Write(t.Segment.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// resolve returns the doc of the page at pagePath, e.g. "/app/forms/42", and
// whether the page has a doc of its own; otherwise it returns the overview.
// Paths with "." or ".." segments never match.
func (l *helpLibrary) resolve(pagePath string) (*helpDoc, bool) {
	var segs []string
	for _, s := range strings.Split(pagePath, "/") {
		switch {
		case s == "":
		case s == "." || s == ".." || strings.ContainsAny(s, `\`):
			return l.docs[helpIndexKey], false
		default:
			segs = append(segs, s)
		}
	}
	if key, ok := l.match("", segs); ok {
		return l.docs[key], true
	}
	return l.docs[helpIndexKey], false
}

// match returns the key of the doc of segs under the directory dir: a
// segment matches the entry of the same name, else any "_param" entry; the
// last segment names a file or a directory with an index.md.
func (l *helpLibrary) match(dir string, segs []string) (string, bool) {
	if len(segs) == 0 {
		key := path.Join(dir, helpIndexKey)
		return key, l.docs[key] != nil
	}
	for _, name := range append([]string{segs[0]}, l.params[dir]...) {
		next := path.Join(dir, name)
		if len(segs) == 1 && l.docs[next] != nil {
			return next, true
		}
		if key, ok := l.match(next, segs[1:]); ok {
			return key, true
		}
	}
	return "", false
}

// get returns the doc of key, e.g. "app/account".
func (l *helpLibrary) get(key string) (*helpDoc, bool) {
	d, ok := l.docs[key]
	return d, ok
}

// helpSection is a group of docs in the manual's table of contents.
type helpSection struct {
	Title string
	Docs  []*helpDoc
}

// toc lists the docs grouped by section, both sorted by their order.
func (l *helpLibrary) toc() []helpSection {
	docs := make([]*helpDoc, 0, len(l.docs))
	for _, d := range l.docs {
		docs = append(docs, d)
	}
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].Order != docs[j].Order {
			return docs[i].Order < docs[j].Order
		}
		return docs[i].Key < docs[j].Key
	})
	var sections []helpSection
	at := map[string]int{}
	for _, d := range docs {
		i, ok := at[d.Section]
		if !ok {
			i = len(sections)
			at[d.Section] = i
			sections = append(sections, helpSection{Title: d.Section})
		}
		sections[i].Docs = append(sections[i].Docs, d)
	}
	return sections
}

// helpIcon renders the button opening the help sidenav at the heading anchor
// of the page's doc; see the "help" partial of layout.html.
func helpIcon(anchor string) template.HTML {
	a := template.HTMLEscapeString(anchor)
	return template.HTML(`<button type="button" data-help="` + a + `" title="Help" aria-label="Help: ` + a + `"` +
		` class="inline-grid h-5 w-5 shrink-0 place-items-center rounded-full border border-slate-700 text-[10px] font-bold text-slate-400 hover:border-indigo-400 hover:text-indigo-300">?</button>`)
}

// helpPage is the data rendered by templates/help.html.
type helpPage struct {
	appLayout
	// Doc is the doc shown; Sections is the table of contents.
	Doc      *helpDoc
	Sections []helpSection
}

// Help renders the manual: the table of contents and the doc named by the
// doc query parameter (a key such as "app/account"), the overview by default.
func (c *appController) Help(w http.ResponseWriter, r *http.Request) {
	doc, ok := helpDocs.get(r.URL.Query().Get("doc"))
	if !ok {
		if r.URL.Query().Has("doc") {
			renderAppMessage(w, r, http.StatusNotFound, "Page not found", "The manual has no such page.")
			return
		}
		doc, _ = helpDocs.get(helpIndexKey)
	}
	renderAppPage(w, r, appHelpTemplate, &helpPage{appLayout: appLayout{Active: "help"}, Doc: doc, Sections: helpDocs.toc()})
}

// HelpFragment serves the doc of the page whose path follows /app/help, e.g.
// /app/help/app/forms/42 for /app/forms/42, as an HTML fragment for the help
// sidenav. The X-Help-Doc header names the doc, so the sidenav can link to it
// in the manual; pages without a doc get the overview.
func (c *appController) HelpFragment(w http.ResponseWriter, r *http.Request) {
	doc, _ := helpDocs.resolve("/" + r.PathValue("path"))
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Help-Doc", doc.Key)
	h.Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(doc.HTML))
}
