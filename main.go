package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/gob"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type BuildProblems struct {
	BazelRule    string
	MissingClass []JavaClass
}

type JavaClass struct {
	Module string // Maven: relative module path
	Layout string // Maven: src/main/java
	Name   string
}

func (a JavaClass) Package() string {
	return StripLast(a.Name)
}

type Dependency struct {
	Name              string
	ExternalReference string
	Resources         []string
}

func die(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// stripLast removes the last '.' and the following segment

func StripLast(s string) string {
	parts := strings.Split(s, ".")
	// strip class name
	parts = parts[0 : len(parts)-1]
	return strings.Join(parts, ".")
}

func problems(scanner bufio.Scanner) BuildProblems {
	const (
		Building  = "Building"
		Compiling = "Compiling Java headers"
		NoPackage = "package (.*) does not exist"
		NoSymbol  = "error: cannot find symbol"
	)
	var (
		REBuilding  = regexp.MustCompile(Building + " lib(.*?)\\.jar ")
		RECompiling = regexp.MustCompile(Compiling +
			" lib(.*?)-hjar\\.jar ")
		REImport       = regexp.MustCompile("import (.*);")
		REImportStatic = regexp.MustCompile("import static (.*);")
	)
	var problems BuildProblems
	// build scanner only knows about missing class names, no module etc.
	add := func(classname string) {
		problems.MissingClass = append(problems.MissingClass,
			JavaClass{Name: classname})
	}
	for scanner.Scan() {
		var line = scanner.Text()
		// Easiest: bazels own suggestions
		if strings.HasPrefix(line, "buildozer ") {
			fmt.Println(line)
			// bazel log will not contain anything else
			os.Exit(0)
		} else if strings.Contains(line, Building) {
			matches := REBuilding.FindStringSubmatch(line)
			if len(matches) == 0 {
				log.Fatalf("expected rule but got %s\n",
					line)
			}
			pkg := matches[1]
			log.Printf("using package name %s\n", pkg)
			problems.BazelRule = pkg
		} else if strings.Contains(line, Compiling) {
			matches := RECompiling.FindStringSubmatch(line)
			if len(matches) == 0 {
				log.Fatalf("expected rule but got %s\n",
					line)
			}
			pkg := matches[1]
			log.Printf("using package name %s\n", pkg)
			problems.BazelRule = pkg
		} else if b, _ := regexp.MatchString(NoPackage, line); b {
			// Parse next line for class in package
			scanner.Scan()
			line = scanner.Text()
			matches := REImportStatic.FindStringSubmatch(line)
			if len(matches) > 0 {
				// Convert Java member to class
				add(StripLast(matches[1]))
			}
			matches = REImport.FindStringSubmatch(line)
			if len(matches) > 0 {
				add(matches[1])
			}
		} else if strings.Contains(line, NoSymbol) {
			scanner.Scan()
			line = scanner.Text()
			matches := REImport.FindStringSubmatch(line)
			if len(matches) > 0 {
				add(matches[1])
			}
		}
	}
	return problems
}

// expect and return exactly one *.jar file
func oneJarFrom(dir string) string {
	fis, err := ioutil.ReadDir(dir)
	die(err)
	var jars []string
	for _, fi := range fis {
		// filter 'sources' classifier
		if strings.HasSuffix(fi.Name(), "-sources.jar") {
			continue
		}
		if strings.HasSuffix(fi.Name(), ".jar") {
			jars = append(jars, fi.Name())
		}
	}
	if len(jars) != 1 {
		log.Fatalf("want exactly one jar file in %s but got %+v\n",
			dir, jars)
	}
	return filepath.Join(dir, jars[0])
}

func canRead(dir string) bool {
	_, err := os.Stat(dir)
	// no need for os.IsNotExist() dance as all we care is if it's there
	if err != nil {
		return false
	}
	return true
}

func content(jar string) []string {
	r, err := zip.OpenReader(jar)
	die(err)
	defer r.Close()
	var files []string
	for _, f := range r.File {
		// Files in jars are / separated, and end in .class
		if strings.HasSuffix(f.Name, ".class") {
			clazz := strings.TrimSuffix(
				strings.Replace(f.Name, "/", ".", -1),
				".class")
			files = append(files, clazz)
		}
	}
	return files
}

// list all classes in external dependencies
func externalDependencyProvider(workspace string) []Dependency {
	var deps []Dependency
	base := bzOutputBase(workspace)
	for _, dep := range bzQueryExternalDependencies(workspace) {
		log.Printf("processing dependency %s\n", dep)
		dir := filepath.Join(
			base,
			"external",
			strings.TrimPrefix(dep, "//external:"),
			"jar")
		// Some external dependencies may be declared, but not
		// used
		if canRead(dir) {
			jar := oneJarFrom(dir)
			fs := content(jar)
			deps = append(deps, Dependency{dep, jar, fs})
		} else {
			log.Printf("skip non-existent dependency %v\n", dep)
		}
	}
	return deps
}

func bzOutputBase(workdir string) string {
	prms := []string{"bazel", "info", "output_base"}
	cmd := exec.Command(prms[0], prms[1:]...)
	cmd.Dir = workdir
	log.Printf("executing %v in %s\n", prms, cmd.Dir)
	buf, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("error: %v\n", err)
		log.Printf("combined output: %s\n", string(buf))
		log.Fatal(err)
	}
	// expect exactly one line, but just to be on the safe side
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 1 {
		log.Fatalf("expected exactly one line but got %+v\n", lines)
	}
	return lines[0]
}

// list of all external dependencies
func bzQueryExternalDependencies(workdir string) (deps []string) {
	// might trigger dependency resolution
	prms := []string{
		"bazel",
		"query",
		"kind(maven_jar, //external:all)"}
	cmd := exec.Command(prms[0], prms[1:]...)
	cmd.Dir = workdir
	log.Printf("executing %v in %s\n", prms, cmd.Dir)
	buf, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("error: %v\n", err)
		log.Printf("combined output: %s\n", string(buf))
		log.Fatal(err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	for scanner.Scan() {
		line := scanner.Text()
		deps = append(deps, line)
	}
	return
}

func bzRuleExists(rule string, workdir string) bool {
	prms := []string{
		"bazel",
		"query",
		rule,
	}
	cmd := exec.Command(prms[0], prms[1:]...)
	cmd.Dir = workdir
	log.Printf("executing %v in %s\n", prms, cmd.Dir)
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			rc := ee.Sys().(interface {
				ExitStatus() int
			}).ExitStatus()
			if rc == 7 {
				// Not found
				return false
			} else {
				log.Fatal(err)
			}
		}
	}
	return true
}

// recursively scan dir for files matching extension
func scan(dir string, extension string) []string {
	log.Printf("recursively scanning %s for %s files\n", dir, extension)
	var files []string
	// filepath.Glob() is not recursive
	f := func(path string, info os.FileInfo, err error) error {
		if strings.HasSuffix(path, extension) {
			files = append(files, path)
		}
		return nil
	}
	filepath.Walk(dir, f)
	log.Printf("found %d files\n", len(files))
	return files
}

// convert a module directory into a rule name
func name(dir string) string {
	// keep a 1:1 relationship between module locations and names
	return strings.Replace(dir, "/", "_", -1)
}

// convert source files from the same source folder
// into single dependencies
// Name is the derived/ suggested rule name
// external reference is the source path into the module, such as
// ui/web/src/main/java
func fromSource(dir string) []Dependency {
	const sep = "/src/main/java/"
	files := scan(dir, ".java")

	// split into module and class name
	var RESrcMainJava = regexp.MustCompile("(.*)" + sep + "(.*)")

	// map of source directory and contained source files
	modules := make(map[string][]string)
	for _, f := range files {
		matches := RESrcMainJava.FindStringSubmatch(f)
		if len(matches) == 3 {
			srcdir := matches[1]
			file := matches[2]
			clazz := strings.TrimSuffix(
				strings.Replace(file, "/", ".", -1),
				".java")
			modules[srcdir] = append(modules[srcdir], clazz)
		} else {
			log.Printf("skip %s, missing %s?\n", f, sep)
		}
	}

	// Convert into dependencies
	var deps []Dependency
	for k, v := range modules {
		deps = append(deps, Dependency{
			Name:              name(k),
			ExternalReference: k + sep,
			Resources:         v,
		})
	}
	return deps
}

func readCache(filename string) []Dependency {
	f, err := os.Open(filename)
	die(err)
	defer f.Close()
	dec := gob.NewDecoder(f)
	var deps []Dependency
	err = dec.Decode(&deps)
	die(err)
	return deps
}

func updateCache(filename string, deps []Dependency) {
	// Gobify
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(deps)
	die(err)
	ioutil.WriteFile(filename, buf.Bytes(), 0644)
	log.Printf("updated cache %s\n", filename)
}

// there's a 1:1 mapping of genrule name to java package name
func findGenrule(javaPackage string, workspace string) *string {
	rule := strings.Replace(javaPackage, ".", "_", -1)
	prms := []string{
		"bazel",
		"query",
		rule,
		"--output=label_kind",
	}
	cmd := exec.Command(prms[0], prms[1:]...)
	cmd.Dir = workspace
	log.Printf("executing %v in %s\n", prms, cmd.Dir)
	buf, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			rc := ee.Sys().(interface {
				ExitStatus() int
			}).ExitStatus()
			if rc == 7 {
				return nil
			} else {
				log.Fatal(err)
			}
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	want := fmt.Sprintf("genrule rule //:%s", rule)
	if len(lines) == 1 && lines[0] == want {
		return &rule
	}
	return nil
}

func findClass(j JavaClass, deps []Dependency) *Dependency {
	log.Printf("looking for dependency providing class %s\n", j.Name)
	for _, d := range deps {
		for _, r := range d.Resources {
			if j.Name == r {
				return &d
			}
		}
	}
	return nil
}

func findSrcs(j JavaClass, workspace string) *string {
	// making use of java package '.' as regexp to find /
	q := fmt.Sprintf("attr('srcs', %s, :all)", j.Name)
	prms := []string{
		"bazel",
		"query",
		q,
	}
	cmd := exec.Command(prms[0], prms[1:]...)
	cmd.Dir = workspace
	log.Printf("executing %v in %s\n", prms, cmd.Dir)
	buf, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) == 1 && strings.HasPrefix(lines[0], "//:") {
		return &lines[0]
	}
	return nil

}

// return buildozer representation
func bdAddDeps(rule string, deps ...string) string {
	return fmt.Sprintf("buildozer 'add deps %s' %s",
		strings.Join(deps, " "), rule)
}

func emit(s string) {
	fmt.Println(s)
}

func bdNewJavaLibrary(d Dependency) []string {
	return []string{
		fmt.Sprintf("buildozer 'new java_library %s' __pkg__",
			d.Name),
		fmt.Sprintf(`buildozer 'set srcs glob(["%s**/*.java"])' %s`,
			d.ExternalReference, d.Name),
	}
}

func main() {
	var (
		update = flag.Bool("update", false,
			"update internal class cache and exit")
		cachefile = flag.String("cachefile", ".healdb",
			"name of cache file")
		workspace = flag.String("workspace", ".", "bazel workspace")
	)
	flag.Parse()
	if *update {
		deps := fromSource(*workspace)
		log.Printf("found %d source dependencies\n", len(deps))
		d2 := externalDependencyProvider(*workspace)
		log.Printf("found %d external dependencies\n", len(d2))
		for _, d := range d2 {
			deps = append(deps, d)
		}
		updateCache(*cachefile, deps)
		// we cannot run bazel build and these internal bazel commands
		// in parallel, so we're done here
		os.Exit(0)
	}
	deps := readCache(*cachefile)
	log.Printf("cache contains %d dependencies\n", len(deps))

	var scanner = bufio.NewScanner(os.Stdin)
	ps := problems(*scanner)
	log.Printf("build problems: %+v\n", ps)

	// Match missing dependencies against providers
	// Performance: process one missing class per Java package only
	packagesResolved := make(map[string]bool)
	done := func(pkg string) {
		packagesResolved[pkg] = true
	}
	for _, p := range ps.MissingClass {
		if packagesResolved[p.Package()] {
			log.Printf("skipping resolution of class %s as "+
				"package %s has already been resolved\n",
				p.Name, p.Package())
			continue
		}
		log.Printf("resolving missing dependency %v\n", p.Name)
		// sources from internal packages/ rules?
		r := findSrcs(p, *workspace)
		if r == nil {
			log.Printf("not provided by an existing rule\n")
		} else {
			emit(bdAddDeps(ps.BazelRule, *r))
			done(p.Package())
			continue
		}
		// dynamically generated via wsimport?
		f := findGenrule(p.Package(), *workspace)
		if f == nil {
			log.Printf("not provided by wsimport genrule\n")
		} else {
			emit(bdAddDeps(ps.BazelRule, *f))
			done(p.Package())
			continue
		}
		e := findClass(p, deps)
		if e == nil {
			log.Printf("not provided by internal (source) or "+
				"external (maven_jar) dependency %s\n", p)
		} else {
			log.Printf("missing class %v provided by %+v\n",
				p.Name, e.Name)
			// Treat external dependencies same as internal
			name := strings.TrimPrefix(e.Name, "//external:")
			if bzRuleExists(name, *workspace) {
				emit(bdAddDeps(ps.BazelRule, name))
			} else {
				for _, cmd := range bdNewJavaLibrary(*e) {
					emit(cmd)
				}
			}
			done(p.Package())
		}
		log.Printf("*sniff* cannot resolve %s\n", p.Name)
	}
}
