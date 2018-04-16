package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"testing"
)

func ExampleStripLast() {
	fmt.Println(StripLast("a.b.c.d"))
	// Output: a.b.c
}

func TestProblems(t *testing.T) {
	buf, err := ioutil.ReadFile("testdata/bazel-1.log")
	die(err)
	probs := problems(*bufio.NewScanner(bytes.NewReader(buf)))
	if len(probs.BazelRule) == 0 {
		log.Fatalf("expected bazel rule but found nothing")
	}
	want := 7
	got := len(probs.MissingClass)
	if want != got {
		log.Fatalf("want %d but got %d\n", want, got)
	}
	for _, m := range probs.MissingClass {
		log.Printf("%+v\n", m)
	}
}

func TestOneJarFrom(t *testing.T) {
	want := "testdata/junit-4.10.jar"
	got := oneJarFrom("testdata")
	if want != got {
		t.Fatalf("want %s but got %s\n", want, got)
	}
}

func TestJarContent(t *testing.T) {
	want := 252
	got := len(content("testdata/junit-4.10.jar"))
	if want != got {
		t.Fatalf("want %v but got %v\n", want, got)
	}
}

func TestBzOutputBase(t *testing.T) {
	s := bzOutputBase("testdata/workspace")
	log.Printf("output base: %s\n", s)
}

func TestExternalDependencies(t *testing.T) {
	want := 2
	got := len(externalDependencyProvider("testdata/workspace"))
	if want != got {
		t.Fatalf("expected %v but got %v\n", want, got)
	}
}

func TestFromSource(t *testing.T) {
	deps := fromSource("testdata/modules")
	log.Printf("deps: %+v\n", deps)
}

func TestPackage(t *testing.T) {
	j := JavaClass{Name: "org.company.framework.A"}
	want := "org.company.framework"
	got := j.Package()
	if want != got {
		t.Fatalf("want %s but got %s\n", want, got)
	}
}
