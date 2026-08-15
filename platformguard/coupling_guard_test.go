package platformguard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// TestNoProductVocabularyInPlatformService is the recurrence guard for
// plan §9.2 / board row 9.1.
//
// §9.1's audit measured every `defolt-*` platform service for
// retail-specific vocabulary and found exactly one coupled service:
// defolt-billing-service, which hardcoded MaxProducts / MaxBranches /
// MaxTills / MaxStaff as columns on Plan. Everything else was already
// product-neutral. That audit is a snapshot, and a snapshot is not an
// invariant: without a guard the second product-specific field enters a
// platform service exactly the way MaxTills did — quietly, in a service
// that had no reason to know what a till is.
//
// The rule this enforces: a service in the platform layer may not name a
// concept that belongs to one product's domain. A clinic has
// practitioners, patients and beds; a shop has tills, products and
// branches. A platform service must be able to serve both without a
// rename, which means it must name neither.
//
// What is scanned: the declared surface of the repository — top-level
// types, struct fields, functions and methods, package-level constants
// and variables — plus the string value of any package-level string
// constant that reads as a token, because a metric name like
// `MetricProducts = "products"` couples the service through its data
// just as firmly as a column would.
//
// What is NOT scanned, and why:
//
//   - comments and prose. §9.1 measured that identity and audit matched
//     "only comment prose". Explaining that a tenant might be a shop is
//     documentation; naming a field `TillCount` is coupling.
//   - function bodies. A local variable is not surface, it is working-out.
//     Scanning them measured as pure noise, and a noisy guard is a
//     deleted guard.
//   - long or multi-line string constants. A SQL block that names an
//     index is describing a schema this service already owns; it is not
//     a new coupling, and matching inside it produced only false
//     positives when measured against defolt-tenants-service.
//   - `_test.go` files. Tests do not ship, and a test that constructs a
//     retail-shaped fixture for a generic code path is legitimate.
//   - `vendor/`. Not our code.
//   - files listed in exemptFiles below.
//
// Two escape hatches exist, both deliberately noisy:
//
//   - exemptFiles — a whole file whose contents are dictated by
//     something outside this service, so no platform design decision is
//     taken in it. Two kinds qualify. One is a product catalogue: P1.0's
//     point is that a product's metrics become rows rather than columns,
//     and those rows still have to be written down somewhere, so a file
//     per product is the honest place — adding a product then means
//     adding a file, not editing platform logic. The other is a third
//     party's wire format: Selcom calls a payee account a "till" and
//     Pesapal sends a "branch", and renaming either would simply break
//     the integration.
//   - allowedIdentifiers — a single identifier that trips the word list
//     for an unrelated reason.
//
// And one ledger, which is not an escape hatch:
//
//   - knownCoupling — coupling that is real, is being paid off, and has
//     a named row that pays it. It is kept separate from
//     allowedIdentifiers so that debt never reads as approval, and so
//     that `len(knownCoupling)` is a number that should only ever fall.
//
// All three are checked for rot: an entry that no longer matches
// anything fails the build. That is what makes the ledger work in the
// direction it is meant to — when P1.0 deletes MaxTills, the entry
// naming MaxTills starts failing, so the row cannot be closed while
// still claiming a debt it has already paid.
//
// Same shape as the RLS guard in the drs-* services, and for the same
// reason given in §9.2: a hand-maintained invariant that nothing checks
// will drift.
func TestNoProductVocabularyInPlatformService(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	fileSeen := map[string]bool{}
	allowSeen := map[string]bool{}
	var findings []finding

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", ".git", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel := filepath.ToSlash(mustRel(root, path))
		if _, ok := exemptFiles[rel]; ok {
			fileSeen[rel] = true
			return nil
		}
		findings = append(findings, scanFile(t, path, rel)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}

	debtSeen := map[string]bool{}
	var reported []finding
	for _, f := range findings {
		key := f.file + "::" + f.ident
		if _, ok := allowedIdentifiers[key]; ok {
			allowSeen[key] = true
			continue
		}
		if _, ok := knownCoupling[key]; ok {
			debtSeen[key] = true
			continue
		}
		reported = append(reported, f)
	}

	sort.Slice(reported, func(i, j int) bool {
		if reported[i].file != reported[j].file {
			return reported[i].file < reported[j].file
		}
		return reported[i].line < reported[j].line
	})

	for _, f := range reported {
		t.Errorf("%s:%d: %s %q names %q, which belongs to one product's domain "+
			"and not to the platform. A platform service must serve a shop and a "+
			"clinic without a rename. Make it data (a keyed row) rather than an "+
			"identifier, or add it to allowedIdentifiers with a reason.",
			f.file, f.line, f.kind, f.ident, f.word)
	}

	for rel, why := range exemptFiles {
		if !fileSeen[rel] {
			t.Errorf("exemptFiles names %q (%s) but no such file exists. "+
				"Remove the entry — an exemption that outlives its file is how the "+
				"next one gets granted without being read.", rel, why)
		}
	}
	for key, why := range allowedIdentifiers {
		if !allowSeen[key] {
			t.Errorf("allowedIdentifiers names %q (%s) but nothing matched it. "+
				"Either the identifier is gone, or it no longer trips the word "+
				"list. Remove the entry.", key, why)
		}
	}
	for key, why := range knownCoupling {
		if !debtSeen[key] {
			t.Errorf("knownCoupling names %q (%s) but nothing matched it — the "+
				"coupling is gone. Delete the entry: a ledger that still lists a "+
				"debt already paid is how the next reader concludes the work was "+
				"never done.", key, why)
		}
	}
}

type finding struct {
	file, kind, ident, word string
	line                    int
}

func scanFile(t *testing.T, path, rel string) []finding {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}

	var out []finding
	report := func(kind, ident string, pos token.Pos) {
		if word, bad := offendingWord(ident); bad {
			out = append(out, finding{
				file: rel, kind: kind, ident: ident, word: word,
				line: fset.Position(pos).Line,
			})
		}
	}

	// Walk the top-level declarations only. Descending into function
	// bodies would report local variables, which are working-out rather
	// than surface.
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			report("func", d.Name.Name, d.Name.Pos())
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					report("type", s.Name.Name, s.Name.Pos())
					// Struct fields nest arbitrarily deep, so this one
					// does need a walk.
					ast.Inspect(s.Type, func(n ast.Node) bool {
						if field, ok := n.(*ast.Field); ok {
							for _, name := range field.Names {
								report("field", name.Name, name.Pos())
							}
						}
						return true
					})
				case *ast.ValueSpec:
					for _, name := range s.Names {
						report("declaration", name.Name, name.Pos())
					}
					// A metric name held as a string constant couples
					// the service through its data, which is exactly
					// the shape P1.0 moves the four cap columns into.
					// Check the value too.
					for _, v := range s.Values {
						lit, ok := v.(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						if val, ok := tokenValue(lit.Value); ok {
							report("string constant", val, lit.Pos())
						}
					}
				}
			}
		}
	}
	return out
}

// tokenValue accepts a string constant only when it reads as a single
// token — a metric name, a header, a context key. A SQL statement or a
// sentence is prose about a schema this service already owns, and
// matching inside one produced only false positives when measured.
func tokenValue(raw string) (string, bool) {
	v := strings.Trim(raw, "`\"")
	if len(v) == 0 || len(v) > 40 || strings.ContainsAny(v, " \t\n\r") {
		return "", false
	}
	return v, true
}

// productVocabulary is the list from §9.2, plus the neighbouring words
// that would carry the same coupling under a different spelling.
//
// Retail is DRS's domain and clinical is AfyaOS's. Both are listed on
// purpose: the guard is not "keep DRS out of the platform", it is "keep
// ANY single product's domain out of the platform", and a guard that
// only knew about the product that exists today would wave the second
// one straight through.
// `checkout` was on this list and was removed on measurement, which is
// worth recording so it is not added back. A Selcom checkout is how the
// platform takes money; defolt-payment-service, defolt-billing-service
// and defolt-tenants-service all name it, and a clinic pays for its
// subscription through exactly the same one. It is platform vocabulary
// that happens to sound retail, and listing it would have produced
// eleven false positives and no true ones.
var productVocabulary = []string{
	// retail — DRS
	"till", "sale", "product", "branch", "sku", "merchant", "cashier",
	"storefront", "catalogue", "restock", "shelf",
	// clinical — AfyaOS
	"patient", "practitioner", "bed", "prescription", "ward",
	"diagnosis", "clinician", "appointment", "triage",
}

// offendingWord splits an identifier into words and returns the first
// one that is product vocabulary.
//
// Splitting rather than substring matching is what keeps the guard
// usable: `Production` is one word and is not `product`, `Storefront`
// is one word and is not `store`. A guard that fires on `Production`
// gets deleted within a week, and then the invariant is gone.
func offendingWord(ident string) (string, bool) {
	for _, w := range splitWords(ident) {
		w = singular(w)
		for _, bad := range productVocabulary {
			if w == bad {
				return bad, true
			}
		}
	}
	return "", false
}

// splitWords breaks CamelCase, snake_case, kebab-case, dotted and
// space-separated identifiers into lowercase words. Runs of capitals
// stay together (`SKU`, `ID`, `HTTPClient` -> http, client).
func splitWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r):
			// Start a new word on lower->upper, and on the last capital
			// of a run that is followed by a lowercase letter, so
			// HTTPClient splits as http + client rather than httpclient.
			prevLower := i > 0 && unicode.IsLower(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			prevUpper := i > 0 && unicode.IsUpper(runes[i-1])
			if prevLower || (prevUpper && nextLower) {
				flush()
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

// singular strips the plural the four cap columns used (`MaxProducts`,
// `MaxBranches`), so the word list can stay singular.
//
// The `-es` case is load-bearing and was found by measurement: a first
// cut handled only `-s`, so `MaxProducts` and `MaxTills` were caught
// while `MaxBranches` and `OverageFromBranches` — the same defect, one
// letter away — went straight through. A guard that catches three of
// four is worse than none, because it reads as a pass.
func singular(w string) string {
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 3:
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "es") && len(w) > 2 && sibilant(w[:len(w)-2]):
		return w[:len(w)-2]
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && len(w) > 1:
		return w[:len(w)-1]
	}
	return w
}

// sibilant reports whether a stem takes `-es` rather than `-s`:
// branch/branches, box/boxes, diagnosis/diagnoses.
func sibilant(stem string) bool {
	for _, suffix := range []string{"ch", "sh", "s", "x", "z"} {
		if strings.HasSuffix(stem, suffix) {
			return true
		}
	}
	return false
}

func mustRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		panic(fmt.Sprintf("relative path for %s: %v", path, err))
	}
	return rel
}
