package dashboardaudit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
)

const (
	viewTimeline = "timeline"
	viewStats    = "stats"
	viewJarvisKB = "jarvis_kb"
)

var targetViews = []string{viewTimeline, viewStats, viewJarvisKB}

type Report struct {
	Views                               map[string]ViewReport `json:"views"`
	BackendEndpointsExclusiveToJarvisKB []string              `json:"backend_endpoints_exclusive_to_jarvis_kb"`
	SharedCodeDoNotDelete               []SharedCode          `json:"shared_code_do_not_delete"`
}

type ViewReport struct {
	CSSClassesExclusive       []string `json:"css_classes_exclusive"`
	JSFunctionsExclusive      []string `json:"js_functions_exclusive"`
	BackendEndpointsExclusive []string `json:"backend_endpoints_exclusive"`
	Notes                     []string `json:"notes,omitempty"`
}

type SharedCode struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Files       []string `json:"files"`
	UsedByViews []string `json:"used_by_views"`
	Note        string   `json:"note"`
}

type frontendFile struct {
	RelPath  string
	FullPath string
	View     string
	Content  string
}

type apiMethod struct {
	Name       string
	Route      string
	HTTPMethod string
}

type routeInfo struct {
	Method  string
	Path    string
	Handler string
	File    string
}

type frontendAnalysis struct {
	viewCSS    map[string][]string
	viewJS     map[string][]string
	shared     []SharedCode
	apiMethods map[string]apiMethod
}

type backendAnalysis struct {
	viewEndpoints map[string][]string
	jarvisKB      []string
	routesByPath  map[string]routeInfo
}

func Analyze(repoRoot string) (*Report, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("abs repo root: %w", err)
	}

	frontend, err := analyzeFrontend(root)
	if err != nil {
		return nil, err
	}

	backend, err := analyzeBackend(root)
	if err != nil {
		return nil, err
	}

	report := &Report{
		Views:                               map[string]ViewReport{},
		BackendEndpointsExclusiveToJarvisKB: append([]string{}, backend.jarvisKB...),
		SharedCodeDoNotDelete:               append([]SharedCode{}, frontend.shared...),
	}

	for _, entry := range frontend.shared {
		if entry.Kind != "js_api" {
			continue
		}
		methodName := strings.TrimPrefix(entry.Name, "web/app.js::App.API.")
		method, ok := frontend.apiMethods[methodName]
		if !ok {
			continue
		}
		route, ok := backend.routesByPath[method.Route]
		if !ok {
			continue
		}
		report.SharedCodeDoNotDelete = append(report.SharedCodeDoNotDelete, SharedCode{
			Kind:        "backend_endpoint",
			Name:        formatRoute(route),
			Files:       dedupeStrings([]string{"internal/jarvis/api.go", route.File}),
			UsedByViews: append([]string{}, entry.UsedByViews...),
			Note:        "Shared Jarvis KB endpoint still consumed outside the Jarvis view.",
		})
	}

	for _, view := range targetViews {
		report.Views[view] = ViewReport{
			CSSClassesExclusive:       append([]string{}, frontend.viewCSS[view]...),
			JSFunctionsExclusive:      append([]string{}, frontend.viewJS[view]...),
			BackendEndpointsExclusive: append([]string{}, backend.viewEndpoints[view]...),
		}
	}

	if len(report.Views[viewTimeline].CSSClassesExclusive) == 0 && len(report.Views[viewTimeline].JSFunctionsExclusive) == 0 {
		report.Views[viewTimeline] = addNote(report.Views[viewTimeline], "No Timeline frontend references remain in current web files; only the backend route is still registered.")
	}
	if len(report.Views[viewStats].CSSClassesExclusive) == 0 && len(report.Views[viewStats].JSFunctionsExclusive) == 0 {
		report.Views[viewStats] = addNote(report.Views[viewStats], "No Stats frontend references remain in current web files; only the backend route is still registered.")
	}
	if len(report.Views[viewJarvisKB].CSSClassesExclusive) == 0 && len(report.Views[viewJarvisKB].JSFunctionsExclusive) == 0 {
		report.Views[viewJarvisKB] = addNote(report.Views[viewJarvisKB], "Jarvis KB currently has no exclusive frontend code under the scanned files.")
	}

	sort.Strings(report.BackendEndpointsExclusiveToJarvisKB)
	report.SharedCodeDoNotDelete = dedupeSharedCode(report.SharedCodeDoNotDelete)
	sortSharedCode(report.SharedCodeDoNotDelete)
	return report, nil
}

func addNote(report ViewReport, note string) ViewReport {
	report.Notes = append(report.Notes, note)
	return report
}

func analyzeFrontend(repoRoot string) (*frontendAnalysis, error) {
	files, err := loadFrontendFiles(repoRoot)
	if err != nil {
		return nil, err
	}

	cssClasses := extractCSSClasses(files)
	jsFunctions := map[string][]string{
		viewTimeline: {},
		viewStats:    {},
		viewJarvisKB: {},
	}
	cssExclusive := map[string][]string{
		viewTimeline: {},
		viewStats:    {},
		viewJarvisKB: {},
	}

	classUsage := map[string]map[string]bool{}
	for className := range cssClasses {
		classUsage[className] = map[string]bool{}
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(className) + `\b`)
		for _, file := range files {
			if strings.HasSuffix(file.RelPath, ".css") {
				continue
			}
			if re.MatchString(file.Content) {
				classUsage[className][file.RelPath] = true
			}
		}
	}

	apiMethods, err := extractAPIMethods(files)
	if err != nil {
		return nil, err
	}

	shared := []SharedCode{}

	for className, usageFiles := range classUsage {
		usageViews := viewsForFiles(files, usageFiles)
		if isExclusiveToView(className, usageViews, viewJarvisKB) {
			cssExclusive[viewJarvisKB] = append(cssExclusive[viewJarvisKB], "."+className)
			continue
		}
		if len(usageViews) > 1 && isTargetSpecificClass(className) && (usageViews[viewJarvisKB] || usageViews[viewTimeline] || usageViews[viewStats]) {
			shared = append(shared, SharedCode{
				Kind:        "css_class",
				Name:        "." + className,
				Files:       append([]string{"web/style.css"}, sortedKeys(usageFiles)...),
				UsedByViews: sortedKeys(usageViews),
				Note:        "Referenced by multiple frontend views.",
			})
		}
	}

	for _, file := range files {
		if file.View == viewJarvisKB {
			for _, name := range extractNamedFunctions(file.Content) {
				jsFunctions[viewJarvisKB] = append(jsFunctions[viewJarvisKB], qualifyJSFunction(file.RelPath, name))
			}
		}
	}

	for _, method := range apiMethods {
		usageViews := apiUsageViews(files, method.Name)
		if routeBelongsToView(method.Route, viewJarvisKB) {
			qualified := qualifyJSFunction("web/app.js", "App.API."+method.Name)
			if len(usageViews) == 1 && usageViews[viewJarvisKB] {
				jsFunctions[viewJarvisKB] = append(jsFunctions[viewJarvisKB], qualified)
			} else if len(usageViews) > 1 {
				shared = append(shared, SharedCode{
					Kind:        "js_api",
					Name:        qualified,
					Files:       append([]string{"web/app.js"}, viewFilesForUsage(files, usageViews)...),
					UsedByViews: sortedKeys(usageViews),
					Note:        "Shared App.API method for Jarvis-backed data.",
				})
			}
		}
		if routeBelongsToView(method.Route, viewTimeline) && len(usageViews) == 1 && usageViews[viewTimeline] {
			jsFunctions[viewTimeline] = append(jsFunctions[viewTimeline], qualifyJSFunction("web/app.js", "App.API."+method.Name))
		}
		if routeBelongsToView(method.Route, viewStats) && len(usageViews) == 1 && usageViews[viewStats] {
			jsFunctions[viewStats] = append(jsFunctions[viewStats], qualifyJSFunction("web/app.js", "App.API."+method.Name))
		}
	}

	for _, view := range targetViews {
		sort.Strings(cssExclusive[view])
		sort.Strings(jsFunctions[view])
	}

	return &frontendAnalysis{
		viewCSS:    cssExclusive,
		viewJS:     jsFunctions,
		shared:     dedupeSharedCode(shared),
		apiMethods: apiMethods,
	}, nil
}

func analyzeBackend(repoRoot string) (*backendAnalysis, error) {
	parser := astpkg.NewParser(nil)
	defer parser.Close()

	jarvisDir := filepath.Join(repoRoot, "internal", "jarvis")
	files, err := parser.ParseDir(context.Background(), jarvisDir)
	if err != nil {
		return nil, fmt.Errorf("parse internal/jarvis: %w", err)
	}

	symbolFiles := map[string]string{}
	for _, file := range files {
		relPath, err := filepath.Rel(repoRoot, file.Path)
		if err != nil {
			relPath = file.Path
		}
		for _, symbol := range file.Symbols {
			symbolFiles[symbol.Name] = filepath.ToSlash(relPath)
		}
	}

	apiSource, err := os.ReadFile(filepath.Join(jarvisDir, "api.go"))
	if err != nil {
		return nil, fmt.Errorf("read internal/jarvis/api.go: %w", err)
	}

	routePattern := regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) ([^"]+)",\s*a\.(\w+)\)`)
	matches := routePattern.FindAllStringSubmatch(string(apiSource), -1)

	viewEndpoints := map[string][]string{
		viewTimeline: {},
		viewStats:    {},
		viewJarvisKB: {},
	}
	jarvisKB := []string{}
	routesByPath := map[string]routeInfo{}

	for _, match := range matches {
		route := routeInfo{
			Method:  match[1],
			Path:    match[2],
			Handler: match[3],
			File:    symbolFiles[match[3]],
		}
		formatted := formatRoute(route)
		routesByPath[route.Path] = route

		switch {
		case routeBelongsToView(route.Path, viewJarvisKB):
			viewEndpoints[viewJarvisKB] = append(viewEndpoints[viewJarvisKB], formatted)
			jarvisKB = append(jarvisKB, formatted)
		case routeBelongsToView(route.Path, viewTimeline):
			viewEndpoints[viewTimeline] = append(viewEndpoints[viewTimeline], formatted)
		case routeBelongsToView(route.Path, viewStats):
			viewEndpoints[viewStats] = append(viewEndpoints[viewStats], formatted)
		}
	}

	for _, view := range targetViews {
		sort.Strings(viewEndpoints[view])
	}
	sort.Strings(jarvisKB)

	return &backendAnalysis{
		viewEndpoints: viewEndpoints,
		jarvisKB:      jarvisKB,
		routesByPath:  routesByPath,
	}, nil
}

func loadFrontendFiles(repoRoot string) ([]frontendFile, error) {
	patterns := []string{
		filepath.Join(repoRoot, "web", "*.html"),
		filepath.Join(repoRoot, "web", "*.css"),
		filepath.Join(repoRoot, "web", "*.js"),
		filepath.Join(repoRoot, "web", "views", "*.js"),
	}

	seen := map[string]bool{}
	var files []frontendFile
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", pattern, err)
		}
		for _, fullPath := range matches {
			if seen[fullPath] {
				continue
			}
			seen[fullPath] = true
			content, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", fullPath, err)
			}
			relPath, err := filepath.Rel(repoRoot, fullPath)
			if err != nil {
				relPath = fullPath
			}
			relPath = filepath.ToSlash(relPath)
			files = append(files, frontendFile{
				RelPath:  relPath,
				FullPath: fullPath,
				View:     classifyFrontendFile(relPath),
				Content:  string(content),
			})
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].RelPath < files[j].RelPath
	})
	return files, nil
}

func classifyFrontendFile(relPath string) string {
	switch relPath {
	case "web/views/jarvis.js":
		return viewJarvisKB
	case "web/views/timeline.js":
		return viewTimeline
	case "web/views/stats.js":
		return viewStats
	case "web/views/overview.js":
		return "overview"
	case "web/views/plans.js":
		return "plans"
	case "web/views/structure.js":
		return "structure"
	default:
		return "shared"
	}
}

func extractCSSClasses(files []frontendFile) map[string]bool {
	classPattern := regexp.MustCompile(`\.([A-Za-z_][A-Za-z0-9_-]*)`)
	classes := map[string]bool{}
	for _, file := range files {
		if !strings.HasSuffix(file.RelPath, ".css") {
			continue
		}
		matches := classPattern.FindAllStringSubmatch(file.Content, -1)
		for _, match := range matches {
			classes[match[1]] = true
		}
	}
	return classes
}

func extractNamedFunctions(content string) []string {
	pattern := regexp.MustCompile(`(?m)^\s*(?:async\s+)?function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	matches := pattern.FindAllStringSubmatch(content, -1)
	seen := map[string]bool{}
	var names []string
	for _, match := range matches {
		if seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		names = append(names, match[1])
	}
	sort.Strings(names)
	return names
}

func extractAPIMethods(files []frontendFile) (map[string]apiMethod, error) {
	var appFile *frontendFile
	for i := range files {
		if files[i].RelPath == "web/app.js" {
			appFile = &files[i]
			break
		}
	}
	if appFile == nil {
		return nil, fmt.Errorf("web/app.js not found")
	}

	methodPattern := regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*:\s*\([^)]*\)\s*=>\s*API\.(get|post)\((.+)\),\s*$`)
	methods := map[string]apiMethod{}
	for _, line := range strings.Split(appFile.Content, "\n") {
		match := methodPattern.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		methods[match[1]] = apiMethod{
			Name:       match[1],
			HTTPMethod: strings.ToUpper(match[2]),
			Route:      simplifyJSRoute(match[3]),
		}
	}
	return methods, nil
}

func simplifyJSRoute(expr string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return expr
	}
	quote := expr[0]
	if quote != '"' && quote != '\'' && quote != '`' {
		return expr
	}
	body := expr[1:]
	idx := strings.IndexByte(body, byte(quote))
	if idx < 0 {
		idx = len(body)
	}
	body = body[:idx]
	if templ := strings.Index(body, "${"); templ >= 0 {
		body = body[:templ]
	}
	return body
}

func apiUsageViews(files []frontendFile, methodName string) map[string]bool {
	pattern := regexp.MustCompile(`App\.API\.` + regexp.QuoteMeta(methodName) + `\b`)
	views := map[string]bool{}
	for _, file := range files {
		if !strings.HasSuffix(file.RelPath, ".js") || file.RelPath == "web/app.js" {
			continue
		}
		if pattern.MatchString(file.Content) {
			views[file.View] = true
		}
	}
	return views
}

func viewsForFiles(files []frontendFile, usageFiles map[string]bool) map[string]bool {
	fileViews := map[string]string{}
	for _, file := range files {
		fileViews[file.RelPath] = file.View
	}
	views := map[string]bool{}
	for relPath := range usageFiles {
		views[fileViews[relPath]] = true
	}
	return views
}

func viewFilesForUsage(files []frontendFile, usageViews map[string]bool) []string {
	var relPaths []string
	for _, file := range files {
		if usageViews[file.View] && strings.HasSuffix(file.RelPath, ".js") {
			relPaths = append(relPaths, file.RelPath)
		}
	}
	sort.Strings(relPaths)
	return relPaths
}

func isExclusiveToView(className string, usageViews map[string]bool, view string) bool {
	if len(usageViews) == 1 && usageViews[view] {
		return true
	}
	if len(usageViews) > 1 {
		return false
	}
	switch view {
	case viewJarvisKB:
		return strings.HasPrefix(className, "jv-") || strings.HasPrefix(className, "ov2-")
	case viewTimeline:
		return strings.HasPrefix(className, "timeline-")
	case viewStats:
		return strings.HasPrefix(className, "stats-")
	default:
		return false
	}
}

func isTargetSpecificClass(className string) bool {
	return strings.HasPrefix(className, "jv-") ||
		strings.HasPrefix(className, "ov2-") ||
		strings.HasPrefix(className, "timeline-") ||
		strings.HasPrefix(className, "stats-")
}

func routeBelongsToView(path, view string) bool {
	switch view {
	case viewJarvisKB:
		return strings.HasPrefix(path, "/api/dashboard/jarvis/")
	case viewTimeline:
		return strings.Contains(path, "/timeline/")
	case viewStats:
		return strings.Contains(path, "/stats/")
	default:
		return false
	}
}

func formatRoute(route routeInfo) string {
	if route.File == "" {
		return fmt.Sprintf("%s %s -> %s", route.Method, route.Path, route.Handler)
	}
	return fmt.Sprintf("%s %s -> %s (%s)", route.Method, route.Path, route.Handler, route.File)
}

func qualifyJSFunction(relPath, name string) string {
	return relPath + "::" + name
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func dedupeSharedCode(entries []SharedCode) []SharedCode {
	type key struct {
		Kind string
		Name string
	}
	seen := map[key]SharedCode{}
	for _, entry := range entries {
		k := key{Kind: entry.Kind, Name: entry.Name}
		existing, ok := seen[k]
		if !ok {
			entry.Files = dedupeStrings(entry.Files)
			entry.UsedByViews = dedupeStrings(entry.UsedByViews)
			seen[k] = entry
			continue
		}
		existing.Files = dedupeStrings(append(existing.Files, entry.Files...))
		existing.UsedByViews = dedupeStrings(append(existing.UsedByViews, entry.UsedByViews...))
		if existing.Note == "" {
			existing.Note = entry.Note
		}
		seen[k] = existing
	}

	out := make([]SharedCode, 0, len(seen))
	for _, entry := range seen {
		out = append(out, entry)
	}
	sortSharedCode(out)
	return out
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortSharedCode(entries []SharedCode) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind == entries[j].Kind {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Kind < entries[j].Kind
	})
}
