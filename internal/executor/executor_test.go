package executor

import (
	"sync"
	"testing"
)

func TestInterpolateStringExactMatchReturnsValue(t *testing.T) {
	exec := &Executor{}
	var outputs sync.Map
	expected := map[string]interface{}{"value": 42}
	outputs.Store("read", expected)

	result, err := exec.interpolateString("${{ steps.read.output }}", &outputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != expected {
		t.Fatalf("expected output to be returned as-is")
	}
}

func TestInterpolateStringEmbedded(t *testing.T) {
	exec := &Executor{}
	var outputs sync.Map
	outputs.Store("read", "hello")

	result, err := exec.interpolateString("File content: ${{ steps.read.output }}", &outputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "File content: hello" {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestInterpolateStringEmbeddedJSON(t *testing.T) {
	exec := &Executor{}
	var outputs sync.Map
	outputs.Store("data", map[string]interface{}{"ok": true})

	result, err := exec.interpolateString("Payload: ${{ steps.data.output }}", &outputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "Payload: {\"ok\":true}" {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestInterpolateStringMultipleReplacements(t *testing.T) {
	exec := &Executor{}
	var outputs sync.Map
	outputs.Store("first", 1)
	outputs.Store("second", "two")

	result, err := exec.interpolateString("A ${{ steps.first.output }} B ${{ steps.second.output }}", &outputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "A 1 B two" {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestInterpolateStringMissingOutput(t *testing.T) {
	exec := &Executor{}
	var outputs sync.Map

	_, err := exec.interpolateString("File: ${{ steps.missing.output }}", &outputs)
	if err == nil {
		t.Fatalf("expected error for missing output")
	}
}
