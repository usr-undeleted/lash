package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type ArrayVar struct {
	Indexed   []string
	Assoc     map[string]string
	IsAssoc   bool
	IsIndexed bool
}

var arrayTable map[string]*ArrayVar
var arrayScopeStack []map[string]*ArrayVar

func initArrayTable() {
	arrayTable = make(map[string]*ArrayVar)
	arrayScopeStack = nil
}

func getArray(name string) *ArrayVar {
	for i := len(arrayScopeStack) - 1; i >= 0; i-- {
		if arr, ok := arrayScopeStack[i][name]; ok {
			return arr
		}
	}
	if arr, ok := arrayTable[name]; ok {
		return arr
	}
	return nil
}

func setArray(name string, arr *ArrayVar) {
	for i := len(arrayScopeStack) - 1; i >= 0; i-- {
		if _, ok := arrayScopeStack[i][name]; ok {
			arrayScopeStack[i][name] = arr
			return
		}
	}
	arrayTable[name] = arr
}

func setArrayElement(name string, index string, value string) {
	arr := getArray(name)
	if arr == nil {
		arr = &ArrayVar{Indexed: []string{}, IsIndexed: true}
	}

	if arr.IsAssoc {
		arr.Assoc[index] = value
	} else {
		idx, err := strconv.Atoi(index)
		if err != nil {
			if arr.Indexed == nil && arr.Assoc == nil {
				arr.Assoc = make(map[string]string)
				arr.IsAssoc = true
				arr.IsIndexed = false
				arr.Assoc[index] = value
			} else {
				fmt.Fprintf(os.Stderr, "lash: %s: cannot use string index on indexed array\n", name)
				return
			}
		} else {
			arr.IsIndexed = true
			for len(arr.Indexed) <= idx {
				arr.Indexed = append(arr.Indexed, "")
			}
			arr.Indexed[idx] = value
		}
	}
	setArray(name, arr)
}

func getArrayElement(name string, index string) string {
	arr := getArray(name)
	if arr == nil {
		return ""
	}
	if arr.IsAssoc {
		return arr.Assoc[index]
	}
	if index[0] == '-' && len(index) > 1 {
		idx, err := strconv.Atoi(index)
		if err != nil {
			return ""
		}
		idx = len(arr.Indexed) + idx
		if idx < 0 || idx >= len(arr.Indexed) {
			return ""
		}
		return arr.Indexed[idx]
	}
	idx, err := strconv.Atoi(index)
	if err != nil {
		return ""
	}
	if idx < 0 || idx >= len(arr.Indexed) {
		return ""
	}
	return arr.Indexed[idx]
}

func getArrayAll(name string) []string {
	arr := getArray(name)
	if arr == nil {
		return nil
	}
	if arr.IsAssoc {
		var result []string
		keys := make([]string, 0, len(arr.Assoc))
		for k := range arr.Assoc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			result = append(result, arr.Assoc[k])
		}
		return result
	}
	result := make([]string, len(arr.Indexed))
	copy(result, arr.Indexed)
	return result
}

func getArrayIndices(name string) []string {
	arr := getArray(name)
	if arr == nil {
		return nil
	}
	if arr.IsAssoc {
		keys := make([]string, 0, len(arr.Assoc))
		for k := range arr.Assoc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}
	indices := make([]string, len(arr.Indexed))
	for i := range arr.Indexed {
		indices[i] = strconv.Itoa(i)
	}
	return indices
}

func getArrayLength(name string) int {
	arr := getArray(name)
	if arr == nil {
		return 0
	}
	if arr.IsAssoc {
		return len(arr.Assoc)
	}
	return len(arr.Indexed)
}

func unsetArray(name string) {
	for i := len(arrayScopeStack) - 1; i >= 0; i-- {
		if _, ok := arrayScopeStack[i][name]; ok {
			delete(arrayScopeStack[i], name)
			return
		}
	}
	delete(arrayTable, name)
}

func isArray(name string) bool {
	return getArray(name) != nil
}

func pushArrayScope() {
	arrayScopeStack = append(arrayScopeStack, make(map[string]*ArrayVar))
}

func popArrayScope() {
	if len(arrayScopeStack) > 0 {
		arrayScopeStack = arrayScopeStack[:len(arrayScopeStack)-1]
	}
}

func snapshotArrays() map[string]*ArrayVar {
	snapshot := make(map[string]*ArrayVar)
	for k, v := range arrayTable {
		cp := *v
		if cp.Assoc != nil {
			cp.Assoc = make(map[string]string, len(v.Assoc))
			for ak, av := range v.Assoc {
				cp.Assoc[ak] = av
			}
		}
		if cp.Indexed != nil {
			cp.Indexed = make([]string, len(v.Indexed))
			copy(cp.Indexed, v.Indexed)
		}
		snapshot[k] = &cp
	}
	return snapshot
}

func restoreArrays(saved map[string]*ArrayVar) {
	arrayTable = make(map[string]*ArrayVar, len(saved))
	for k, v := range saved {
		cp := *v
		if cp.Assoc != nil {
			cp.Assoc = make(map[string]string, len(v.Assoc))
			for ak, av := range v.Assoc {
				cp.Assoc[ak] = av
			}
		}
		if cp.Indexed != nil {
			cp.Indexed = make([]string, len(v.Indexed))
			copy(cp.Indexed, v.Indexed)
		}
		arrayTable[k] = &cp
	}
}

func parseArrayLiteral(s string) []string {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return nil
	}

	var result []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				current.WriteByte(ch)
			}
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = true
			continue
		}

		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}

		if inDouble {
			if ch == '\\' && i+1 < len(s) {
				next := s[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					current.WriteByte(next)
					i++
					continue
				}
			}
			current.WriteByte(ch)
			continue
		}

		if ch == ' ' || ch == '\t' || ch == '\n' {
			result = append(result, current.String())
			current.Reset()
			continue
		}

		current.WriteByte(ch)
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

func parseAssocLiteral(s string) map[string]string {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return nil
	}

	result := make(map[string]string)
	var current strings.Builder
	inSingle := false
	inDouble := false
	var parts []string

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				current.WriteByte(ch)
			}
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = true
			continue
		}

		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}

		if inDouble {
			if ch == '\\' && i+1 < len(s) {
				next := s[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					current.WriteByte(next)
					i++
					continue
				}
			}
			current.WriteByte(ch)
			continue
		}

		if ch == ' ' || ch == '\t' || ch == '\n' {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}

		current.WriteByte(ch)
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	for _, p := range parts {
		eqIdx := strings.Index(p, "=")
		if eqIdx < 0 {
			continue
		}
		key := p[:eqIdx]
		val := p[eqIdx+1:]
		result[key] = val
	}

	return result
}
