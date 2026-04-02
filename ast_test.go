package main

import (
	"testing"
)

func TestParseSimple(t *testing.T) {
	prog := Parse("echo hello world")
	if len(prog.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(prog.Commands))
	}
	cmd, ok := prog.Commands[0].(*Command)
	if !ok {
		t.Fatalf("expected *Command, got %T", prog.Commands[0])
	}
	if len(cmd.Args) != 3 || cmd.Args[0] != "echo" || cmd.Args[1] != "hello" || cmd.Args[2] != "world" {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}
}

func TestParsePipeline(t *testing.T) {
	prog := Parse("echo hello | cat | wc")
	if len(prog.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(prog.Commands))
	}
	pipe, ok := prog.Commands[0].(*Pipeline)
	if !ok {
		t.Fatalf("expected *Pipeline, got %T", prog.Commands[0])
	}
	if len(pipe.Commands) != 3 {
		t.Fatalf("expected 3 pipeline commands, got %d", len(pipe.Commands))
	}
}

func TestParseAndOr(t *testing.T) {
	prog := Parse("true && echo yes || echo no")
	if len(prog.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(prog.Commands))
	}
	ao, ok := prog.Commands[0].(*AndOr)
	if !ok {
		t.Fatalf("expected *AndOr, got %T", prog.Commands[0])
	}
	if ao.Op != "||" {
		t.Fatalf("expected ||, got %s", ao.Op)
	}
	left, ok := ao.Left.(*AndOr)
	if !ok {
		t.Fatalf("expected nested *AndOr, got %T", ao.Left)
	}
	if left.Op != "&&" {
		t.Fatalf("expected &&, got %s", left.Op)
	}
}

func TestParseSemicolon(t *testing.T) {
	prog := Parse("echo a; echo b; echo c")
	if len(prog.Commands) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(prog.Commands))
	}
}

func TestParseBackground(t *testing.T) {
	prog := Parse("sleep 1 &")
	cmd, ok := prog.Commands[0].(*Command)
	if !ok {
		t.Fatalf("expected *Command, got %T", prog.Commands[0])
	}
	if !cmd.Background {
		t.Fatal("expected background=true")
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "sleep" || cmd.Args[1] != "1" {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}
}

func TestParseRedirect(t *testing.T) {
	prog := Parse("echo hi > out.txt")
	cmd, ok := prog.Commands[0].(*Command)
	if !ok {
		t.Fatalf("expected *Command, got %T", prog.Commands[0])
	}
	if len(cmd.Redirections) != 1 || cmd.Redirections[0].Op != ">" || cmd.Redirections[0].Target != "out.txt" {
		t.Fatalf("unexpected redirections: %v", cmd.Redirections)
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "echo" {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}
}

func TestParseAppendRedirect(t *testing.T) {
	prog := Parse("echo hi >> out.txt")
	cmd := prog.Commands[0].(*Command)
	if len(cmd.Redirections) != 1 || cmd.Redirections[0].Op != ">>" {
		t.Fatalf("unexpected redirections: %v", cmd.Redirections)
	}
}

func TestParseAssignWithCmd(t *testing.T) {
	prog := Parse("X=foo echo $X")
	cmd := prog.Commands[0].(*Command)
	if len(cmd.Assignments) != 1 || cmd.Assignments[0].Name != "X" || cmd.Assignments[0].Value != "foo" {
		t.Fatalf("unexpected assignments: %v", cmd.Assignments)
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "echo" {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}
}

func TestParseAssignOnly(t *testing.T) {
	prog := Parse("X=foo")
	cmd := prog.Commands[0].(*Command)
	if len(cmd.Assignments) != 1 || cmd.Assignments[0].Name != "X" || cmd.Assignments[0].Value != "foo" {
		t.Fatalf("unexpected assignments: %v", cmd.Assignments)
	}
	if len(cmd.Args) != 0 {
		t.Fatalf("expected no args, got %v", cmd.Args)
	}
}

func TestParseIf(t *testing.T) {
	prog := Parse("if true; then echo yes; fi")
	if len(prog.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(prog.Commands))
	}
	ifStmt, ok := prog.Commands[0].(*IfStmt)
	if !ok {
		t.Fatalf("expected *IfStmt, got %T", prog.Commands[0])
	}
	if ifStmt.Else != nil {
		t.Fatal("expected no else branch")
	}
	if len(ifStmt.Elifs) != 0 {
		t.Fatalf("expected no elif branches, got %d", len(ifStmt.Elifs))
	}
}

func TestParseIfElse(t *testing.T) {
	prog := Parse("if true; then echo yes; else echo no; fi")
	ifStmt := prog.Commands[0].(*IfStmt)
	if ifStmt.Else == nil {
		t.Fatal("expected else branch")
	}
}

func TestParseIfElifElse(t *testing.T) {
	prog := Parse("if false; then echo a; elif true; then echo b; else echo c; fi")
	ifStmt := prog.Commands[0].(*IfStmt)
	if len(ifStmt.Elifs) != 1 {
		t.Fatalf("expected 1 elif branch, got %d", len(ifStmt.Elifs))
	}
	if ifStmt.Else == nil {
		t.Fatal("expected else branch")
	}
}

func TestParseWhile(t *testing.T) {
	prog := Parse("while true; do echo hi; done")
	if len(prog.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(prog.Commands))
	}
	ws, ok := prog.Commands[0].(*WhileStmt)
	if !ok {
		t.Fatalf("expected *WhileStmt, got %T", prog.Commands[0])
	}
	if ws.Until {
		t.Fatal("expected until=false")
	}
}

func TestParseUntil(t *testing.T) {
	prog := Parse("until false; do echo hi; done")
	ws := prog.Commands[0].(*WhileStmt)
	if !ws.Until {
		t.Fatal("expected until=true")
	}
}

func TestParseFor(t *testing.T) {
	prog := Parse("for x in a b c; do echo $x; done")
	if len(prog.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(prog.Commands))
	}
	fs, ok := prog.Commands[0].(*ForStmt)
	if !ok {
		t.Fatalf("expected *ForStmt, got %T", prog.Commands[0])
	}
	if fs.Var != "x" {
		t.Fatalf("expected var=x, got %s", fs.Var)
	}
	if len(fs.Words) != 3 {
		t.Fatalf("expected 3 words, got %v", fs.Words)
	}
}

func TestParseCase(t *testing.T) {
	prog := Parse("case $x in a) echo A ;; b) echo B ;; esac")
	if len(prog.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(prog.Commands))
	}
	cs, ok := prog.Commands[0].(*CaseStmt)
	if !ok {
		t.Fatalf("expected *CaseStmt, got %T", prog.Commands[0])
	}
	if cs.Word != "$x" {
		t.Fatalf("expected word=$x, got %s", cs.Word)
	}
	if len(cs.Branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(cs.Branches))
	}
	if len(cs.Branches[0].Patterns) != 1 || cs.Branches[0].Patterns[0] != "a" {
		t.Fatalf("unexpected branch 0 patterns: %v", cs.Branches[0].Patterns)
	}
}

func TestParseComplex(t *testing.T) {
	prog := Parse("if true; then echo a | cat; echo b && echo c; else echo d; fi")
	ifStmt := prog.Commands[0].(*IfStmt)
	if ifStmt.Body == nil {
		t.Fatal("expected body")
	}
	bodyProg, ok := ifStmt.Body.(*Program)
	if !ok {
		t.Fatalf("expected body to be *Program, got %T", ifStmt.Body)
	}
	if len(bodyProg.Commands) != 2 {
		t.Fatalf("expected 2 commands in if body, got %d", len(bodyProg.Commands))
	}
	_, isPipeline := bodyProg.Commands[0].(*Pipeline)
	if !isPipeline {
		t.Fatalf("expected first body command to be *Pipeline, got %T", bodyProg.Commands[0])
	}
	_, isAndOr := bodyProg.Commands[1].(*AndOr)
	if !isAndOr {
		t.Fatalf("expected second body command to be *AndOr, got %T", bodyProg.Commands[1])
	}
}

func TestParsePipelineBackground(t *testing.T) {
	prog := Parse("cat file | sort | uniq &")
	pipe, ok := prog.Commands[0].(*Pipeline)
	if !ok {
		t.Fatalf("expected *Pipeline, got %T", prog.Commands[0])
	}
	last := pipe.Commands[len(pipe.Commands)-1].(*Command)
	if !last.Background {
		t.Fatal("expected last command in pipeline to have background=true")
	}
}

func TestParseNegatedPipeline(t *testing.T) {
	prog := Parse("! true")
	pipe, ok := prog.Commands[0].(*Pipeline)
	if !ok {
		t.Fatalf("expected *Pipeline, got %T", prog.Commands[0])
	}
	if !pipe.Negated {
		t.Fatal("expected negated=true")
	}
}
