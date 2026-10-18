// Command architecture checks source-level package boundaries without loading dependencies.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type baseline struct {
	Violations            []string `json:"violations"`
	GatewayStoreMembers   []string `json:"gatewayStoreMembers"`
	EffectiveStoreMethods []string `json:"effectiveStoreMethods"`
}

type definition struct {
	expression ast.Expr
	imports    map[string]string
}

type inventory struct {
	Imports map[string]map[string]bool
	Members []string
	Metrics map[string]int
	Types   map[string]definition
}

func scan(root string) (inventory, error) {
	result := inventory{Imports: map[string]map[string]bool{}, Metrics: map[string]int{}, Types: map[string]definition{}}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		pkg := filepath.ToSlash(filepath.Dir(rel))
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			return err
		}
		if result.Imports[pkg] == nil {
			result.Imports[pkg] = map[string]bool{}
		}
		aliases := map[string]string{}
		for _, spec := range file.Imports {
			value, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			result.Imports[pkg][value] = true
			alias := filepath.Base(value)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			aliases[alias] = value
		}
		if pkg == "internal/app" || pkg == "internal/repository" {
			result.Metrics[pkg+"/files"]++
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result.Metrics[pkg+"/lines"] += bytes.Count(data, []byte("\n"))
		}
		for _, decl := range file.Decls {
			if method, ok := decl.(*ast.FuncDecl); ok && method.Recv != nil && pkg == "internal/app" {
				var buf bytes.Buffer
				if err := printer.Fprint(&buf, set, method.Recv.List[0].Type); err != nil {
					return err
				}
				if strings.TrimPrefix(buf.String(), "*") == "generatedRepositoryAPIAdapter" {
					result.Metrics["managementAdapterMethods"]++
				}
			}
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				typ, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				result.Types[pkg+"."+typ.Name.Name] = definition{expression: typ.Type, imports: aliases}
				if typ.Name.Name != "GatewayStore" || pkg != "internal/app" {
					continue
				}
				iface, ok := typ.Type.(*ast.InterfaceType)
				if !ok {
					return fmt.Errorf("GatewayStore must be an interface")
				}
				for _, field := range iface.Methods.List {
					var buf bytes.Buffer
					if err := printer.Fprint(&buf, set, field.Type); err != nil {
						return err
					}
					name := ""
					for _, ident := range field.Names {
						name += ident.Name + " "
					}
					result.Members = append(result.Members, name+buf.String())
				}
			}
		}
		if pkg == "internal/app" {
			ast.Inspect(file, func(node ast.Node) bool {
				clause, ok := node.(*ast.CaseClause)
				if !ok {
					return true
				}
				for _, expression := range clause.List {
					selector, ok := expression.(*ast.SelectorExpr)
					if ok && strings.HasPrefix(selector.Sel.Name, "Format") {
						result.Metrics["appFormatCases"]++
						break
					}
				}
				return true
			})
		}
		return nil
	})
	sort.Strings(result.Members)
	result.Metrics["gatewayStoreMembers"] = len(result.Members)
	return result, err
}

func formatPackage(pkg string) string {
	parts := strings.Split(pkg, "/")
	if len(parts) < 3 || parts[0] != "internal" {
		return ""
	}
	if parts[1] == "protocol" && parts[2] == "identity" {
		return ""
	}
	if parts[1] == "protocol" || parts[1] == "format" {
		return parts[2]
	}
	return ""
}

func violations(inv inventory, module string) []string {
	found := map[string]bool{}
	for source := range inv.Imports {
		sourceFormat := formatPackage(source)
		if sourceFormat == "" {
			continue
		}
		// Follow internal imports: an intermediate helper must not hide a reverse dependency.
		visited := map[string]bool{}
		var walk func(string)
		walk = func(pkg string) {
			if visited[pkg] {
				return
			}
			visited[pkg] = true
			for target := range inv.Imports[pkg] {
				target = strings.TrimPrefix(target, module+"/")
				forbidden := target == "internal/app" || strings.HasPrefix(target, "internal/app/") ||
					target == "internal/repository/postgres" || strings.HasPrefix(target, "internal/repository/postgres/") ||
					strings.Contains(target, "/pgstore") ||
					(formatPackage(target) != "" && formatPackage(target) != sourceFormat)
				if forbidden {
					found[pkg+" -> "+target] = true
				}
				if strings.HasPrefix(target, "internal/") {
					walk(target)
				}
			}
		}
		walk(source)
	}
	result := []string{}
	for value := range found {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// effectiveMembers follows embedded interfaces, including aliases, using source ASTs.
func effectiveMembers(inv inventory, module string) ([]string, error) {
	result := []string{}
	visited := map[string]bool{}
	var expand func(string) error
	expand = func(key string) error {
		if visited[key] {
			return nil
		}
		visited[key] = true
		def, ok := inv.Types[key]
		if !ok {
			return fmt.Errorf("unresolved store interface %s", key)
		}
		pkg := key[:strings.LastIndex(key, ".")]
		var expression func(ast.Expr) error
		expression = func(expr ast.Expr) error {
			switch value := expr.(type) {
			case *ast.InterfaceType:
				for _, field := range value.Methods.List {
					if len(field.Names) == 0 {
						if err := expression(field.Type); err != nil {
							return err
						}
						continue
					}
					var buf bytes.Buffer
					if err := printer.Fprint(&buf, token.NewFileSet(), field.Type); err != nil {
						return err
					}
					for _, name := range field.Names {
						result = append(result, key+"."+name.Name+" "+buf.String())
					}
				}
				return nil
			case *ast.Ident:
				return expand(pkg + "." + value.Name)
			case *ast.SelectorExpr:
				qualifier, ok := value.X.(*ast.Ident)
				if !ok {
					return fmt.Errorf("unsupported store qualifier in %s", key)
				}
				target, ok := def.imports[qualifier.Name]
				if !ok {
					return fmt.Errorf("unknown store import in %s", key)
				}
				return expand(strings.TrimPrefix(target, module+"/") + "." + value.Sel.Name)
			default:
				return fmt.Errorf("unsupported embedded store interface in %s", key)
			}
		}
		return expression(def.expression)
	}
	if _, ok := inv.Types["internal/app.GatewayStore"]; ok {
		if err := expand("internal/app.GatewayStore"); err != nil {
			return nil, err
		}
	}
	sort.Strings(result)
	return result, nil
}

func additions(current, allowed []string) []string {
	known := map[string]bool{}
	for _, item := range allowed {
		known[item] = true
	}
	result := []string{}
	for _, item := range current {
		if !known[item] {
			result = append(result, item)
		}
	}
	return result
}

func run(root string, metrics bool) error {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 || fields[0] != "module" {
		return fmt.Errorf("missing module declaration")
	}
	inv, err := scan(root)
	if err != nil {
		return err
	}
	methods, err := effectiveMembers(inv, fields[1])
	if err != nil {
		return err
	}
	current := baseline{Violations: violations(inv, fields[1]), GatewayStoreMembers: inv.Members, EffectiveStoreMethods: methods}
	if metrics {
		output, err := json.MarshalIndent(inv.Metrics, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(output))
		return nil
	}
	data, err = os.ReadFile(filepath.Join(root, "tools/architecture/baseline.json"))
	if err != nil {
		return err
	}
	var allowed baseline
	if err := json.Unmarshal(data, &allowed); err != nil {
		return err
	}
	failures := additions(current.Violations, allowed.Violations)
	for _, method := range additions(current.EffectiveStoreMethods, allowed.EffectiveStoreMethods) {
		failures = append(failures, "GatewayStore adds effective method "+method)
	}
	for _, method := range additions(allowed.EffectiveStoreMethods, current.EffectiveStoreMethods) {
		failures = append(failures, "remove retired effective method from baseline: "+method)
	}
	for _, member := range additions(current.GatewayStoreMembers, allowed.GatewayStoreMembers) {
		failures = append(failures, "GatewayStore adds "+member)
	}
	// Removed debt must leave the allowlist, so it cannot be silently reintroduced later.
	for _, stale := range additions(allowed.Violations, current.Violations) {
		failures = append(failures, "remove resolved violation from baseline: "+stale)
	}
	for _, stale := range additions(allowed.GatewayStoreMembers, current.GatewayStoreMembers) {
		failures = append(failures, "remove retired GatewayStore member from baseline: "+stale)
	}
	if len(failures) > 0 {
		return fmt.Errorf("architecture boundary violations:\n%s", strings.Join(failures, "\n"))
	}
	fmt.Println("architecture boundaries: pass")
	return nil
}

func main() {
	root := flag.String("root", ".", "repository root")
	metrics := flag.Bool("metrics", false, "print repeatable source metrics instead of checking")
	flag.Parse()
	if err := run(*root, *metrics); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
