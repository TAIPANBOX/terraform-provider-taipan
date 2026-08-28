// The declaration in components.json is only worth reading if this repository
// proves it, and proves it against the code rather than by describing.
//
// estate-gates cannot do this. It has no Go toolchain, and building twenty-two
// repositories in its CI is a matrix it does not have. This repository already
// runs its suite on every push.
//
// What is proved here is exactly the `checked` bucket and nothing else. The
// `declared` bucket is not asserted against anything, on purpose: a test that
// pretended to verify a sentence about purpose would be the failure this whole
// design exists to avoid.
package manifest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type component struct {
	Name    string `json:"name"`
	Class   string `json:"class"`
	Checked struct {
		Package            string         `json:"package"`
		ProviderTypeName   string         `json:"provider_type_name"`
		Resources          []string       `json:"resources"`
		DataSources        []string       `json:"data_sources"`
		Env                map[string]any `json:"env"`
		ReadsNoEnvironment bool           `json:"reads_no_environment"`
	} `json:"checked"`
}

type manifest struct {
	Schema     string      `json:"schema"`
	Repo       string      `json:"repo"`
	Module     string      `json:"module"`
	Components []component `json:"components"`
}

func root(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locating the repository root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func load(t *testing.T) (manifest, string) {
	t.Helper()
	r := root(t)
	b, err := os.ReadFile(filepath.Join(r, "components.json"))
	if err != nil {
		t.Fatalf("reading components.json: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parsing components.json: %v", err)
	}
	if len(m.Components) == 0 {
		t.Fatal("components.json declares no component, so every test here measured nothing")
	}
	return m, r
}

// Reads every non-test .go file under internal/provider, which is where the
// surface is defined.
func providerSource(t *testing.T, r string) string {
	t.Helper()
	dir := filepath.Join(r, "internal", "provider")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var b strings.Builder
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		b.Write(body)
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		t.Fatal("internal/provider has no non-test source, so this measured nothing")
	}
	return b.String()
}

// THE ONE THAT CLOSES THE HOLE.
func TestEveryBinaryThisRepositoryBuildsIsDeclaredAndTheReverse(t *testing.T) {
	m, r := load(t)

	list := exec.Command("go", "list", "-f", "{{if eq .Name \"main\"}}{{.ImportPath}}{{end}}", "./...")
	list.Dir = r
	out, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	built := map[string]bool{}
	for _, line := range strings.Fields(string(out)) {
		built[line] = true
	}
	if len(built) == 0 {
		t.Fatal("go list found no main package, so this measured nothing")
	}

	declared := map[string]bool{}
	for _, c := range m.Components {
		if c.Checked.Package == "" {
			t.Errorf("component %q declares no package", c.Name)
			continue
		}
		declared[c.Checked.Package] = true
	}
	for p := range built {
		if !declared[p] {
			t.Errorf("this repository builds %s and components.json does not declare it", p)
		}
	}
	for p := range declared {
		if !built[p] {
			t.Errorf("components.json declares %s and this repository does not build it", p)
		}
	}
}

// The declared module path is the one go.mod declares, and the BINARY name that
// falls out of it is what Terraform looks for.
//
// Terraform finds a plugin by the exact filename `terraform-provider-<name>`.
// That is protocol, not taste: the module's last element IS the binary name, so
// a rename of either breaks discovery silently at the point somebody runs
// `terraform init`.
func TestTheModulePathIsTheOneGoModDeclaresAndEndsInTheBinaryTerraformLooksFor(t *testing.T) {
	m, r := load(t)
	if m.Module == "" {
		t.Fatal("components.json records no module path, so this measured nothing")
	}
	b, err := os.ReadFile(filepath.Join(r, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	found := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindSubmatch(b)
	if found == nil {
		t.Fatal("go.mod declares no module, so this measured nothing")
	}
	if got := string(found[1]); got != m.Module {
		t.Fatalf("components.json says the module is %q; go.mod says %q", m.Module, got)
	}

	last := m.Module[strings.LastIndex(m.Module, "/")+1:]
	var typeName string
	for _, c := range m.Components {
		if c.Checked.ProviderTypeName != "" {
			typeName = c.Checked.ProviderTypeName
			break
		}
	}
	if typeName == "" {
		t.Fatal("no component declares a provider type name, so this measured nothing")
	}
	if want := "terraform-provider-" + typeName; last != want {
		t.Errorf("Terraform looks for a plugin binary called %q and this module ends in %q.\n"+
			"The last element of the module path is the binary name, so these cannot differ.",
			want, last)
	}
}

// The declared type name is the one the provider reports to Terraform. Every
// resource address a user writes begins with it.
func TestTheDeclaredProviderTypeNameIsTheOneTheProviderReports(t *testing.T) {
	m, r := load(t)
	src := providerSource(t, r)

	found := regexp.MustCompile(`resp\.TypeName = "([a-z_]+)"`).FindStringSubmatch(src)
	if found == nil {
		t.Fatal("the provider no longer sets a literal TypeName, so this measured nothing")
	}
	checked := 0
	for _, c := range m.Components {
		if c.Checked.ProviderTypeName == "" {
			continue
		}
		checked++
		if c.Checked.ProviderTypeName != found[1] {
			t.Errorf("components.json says the provider type name is %q; the provider reports %q",
				c.Checked.ProviderTypeName, found[1])
		}
	}
	if checked == 0 {
		t.Fatal("no component declares a provider type name, so this measured nothing")
	}
}

// Every resource the provider defines against every one declared, BOTH WAYS.
//
// A resource is what a user's configuration names, so one added in code and not
// here is a public surface nobody declared.
func TestEveryResourceThisProviderDefinesIsDeclaredAndTheReverse(t *testing.T) {
	m, r := load(t)
	src := providerSource(t, r)

	var typeName string
	for _, c := range m.Components {
		if c.Checked.ProviderTypeName != "" {
			typeName = c.Checked.ProviderTypeName
			break
		}
	}
	inCode := map[string]bool{}
	for _, hit := range regexp.MustCompile(`resp\.TypeName = req\.ProviderTypeName \+ "(_[a-z_]+)"`).
		FindAllStringSubmatch(src, -1) {
		inCode[typeName+hit[1]] = true
	}
	if len(inCode) == 0 {
		t.Fatal("no resource sets a TypeName off ProviderTypeName, so this measured nothing")
	}

	declared := map[string]bool{}
	for _, c := range m.Components {
		for _, res := range c.Checked.Resources {
			declared[res] = true
		}
	}
	if len(declared) == 0 {
		t.Fatal("no component declares a resource, so this measured nothing")
	}

	var missing, extra []string
	for name := range inCode {
		if !declared[name] {
			missing = append(missing, name)
		}
	}
	for name := range declared {
		if !inCode[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	for _, n := range missing {
		t.Errorf("the provider defines the resource %s and components.json does not declare it.\n"+
			"That is a surface a user's configuration can name and nothing announced.", n)
	}
	for _, n := range extra {
		t.Errorf("components.json declares the resource %s and the provider defines no such thing", n)
	}
}

// Zero data sources is a fact worth pinning, not an absence worth ignoring.
//
// The day DataSources stops returning an empty slice, the manifest should have
// to say so rather than staying quietly true.
func TestTheDeclaredDataSourceSetIsTheOneTheProviderReturns(t *testing.T) {
	m, r := load(t)
	src := providerSource(t, r)

	empty := regexp.MustCompile(`func \(p \*\w+\) DataSources\([^)]*\) \[\]func\(\) datasource\.DataSource \{\s*return \[\]func\(\) datasource\.DataSource\{\}`).
		MatchString(src)
	if !strings.Contains(src, "DataSources(") {
		t.Fatal("the provider defines no DataSources method, so this measured nothing")
	}

	checked := 0
	for _, c := range m.Components {
		if c.Checked.ProviderTypeName == "" {
			continue
		}
		checked++
		if len(c.Checked.DataSources) == 0 && !empty {
			t.Errorf("components.json declares no data source and the provider's " +
				"DataSources no longer returns an empty slice. Whatever it returns now " +
				"is a surface this file has to name.")
		}
		if len(c.Checked.DataSources) > 0 && empty {
			t.Errorf("components.json declares %d data source(s) and the provider returns none",
				len(c.Checked.DataSources))
		}
	}
	if checked == 0 {
		t.Fatal("no component to judge, so this measured nothing")
	}
}

// Terraform passes configuration through the provider block, not the
// environment, so there is nothing to declare. The reader is proved against a
// planted name first, so "found none" and "cannot find any" differ.
func TestItReadsNoEnvironmentAndTheReaderStillWorks(t *testing.T) {
	m, r := load(t)

	name := regexp.MustCompile(`TAIPAN_[A-Z0-9_]+`)
	if got := name.FindAllString(`const n = "TAIPAN_PLANTED"`, -1); len(got) != 1 {
		t.Fatalf("the reader found %v in a string containing exactly one name, so a "+
			"finding of none below would prove nothing", got)
	}

	var found []string
	err := filepath.Walk(r, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "vendor" {
				return filepath.SkipDir
			}
			if path == filepath.Join(r, "internal", "manifest") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, n := range name.FindAllString(string(b), -1) {
			found = append(found, n+" in "+strings.TrimPrefix(path, r+"/"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}

	for _, c := range m.Components {
		if !c.Checked.ReadsNoEnvironment {
			continue
		}
		for _, f := range found {
			t.Errorf("components.json says this repository reads no environment variable, "+
				"and here is one: %s", f)
		}
		if len(c.Checked.Env) != 0 {
			t.Errorf("components.json claims reads_no_environment and also declares %d "+
				"variable(s). Those cannot both be true.", len(c.Checked.Env))
		}
	}
}
