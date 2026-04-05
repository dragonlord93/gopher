package myjson

import (
	"strings"
)

type Item struct {
	prefix string
	data   map[string]any
}

func Unmarshal(rawJsonStr string) map[string]any {
	result := make(map[string]any)
	s := strings.TrimSpace(rawJsonStr)

	var key, value string
	var depth int
	var nestedStart int
	expectingValue := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '{':
			depth++
			if depth == 2 {
				nestedStart = i
			}
		case '}':
			depth--
			if depth == 0 {
				if key != "" && value != "" {
					result[key] = value
				}
				break
			} else if depth == 1 {
				result[key] = Unmarshal(s[nestedStart : i+1])
				key, value = "", ""
			}
		case ':':
			expectingValue = true
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\n' || s[j] == '\t') {
				j++
			}
			if s[j] != '"' && s[j] != '{' {
				val, end := extractUnquoted(s, j)
				value = val
				result[key] = value
				key, value = "", ""
				i = end
				expectingValue = false
			}
		case '"':
			if expectingValue {
				j := strings.Index(s[i+1:], `"`)
				value = s[i+1 : i+j+1]
				i += j + 1
				expectingValue = false
				//inValue = false
			} else if depth == 1 {
				j := strings.Index(s[i+1:], `"`)
				key = s[i+1 : i+j+1]
				i += j + 1
			}
		case ',':
			if depth == 1 && value != "" {
				result[key] = value
			}
		}
	}

	return result
}

func flattenJson(unflattened map[string]any) map[string]any {
	st := make([]Item, 0)
	result := make(map[string]any)
	st = append(st, Item{prefix: "", data: unflattened})
	for len(st) > 0 {
		it := st[len(st)-1]
		st = st[:len(st)-1]
		for key, val := range it.data {
			fullKey := key
			if it.prefix != "" {
				fullKey = it.prefix + "." + key
			}
			if nested, ok := val.(map[string]any); ok {
				st = append(st, Item{fullKey, nested})
			} else {
				result[fullKey] = val
			}
		}
	}

	return result
}

func unflattenJsonHelper(key string, value any, result map[string]any) {
	tokens := strings.Split(key, ".")
	for i := 0; i < len(tokens)-1; i++ {
		if _, ok := result[tokens[i]]; !ok {
			result[tokens[i]] = map[string]any{}
		}
		result = result[tokens[i]].(map[string]any)
	}
	result[tokens[len(tokens)-1]] = value
}

func unflattenJson(flatenned map[string]any) map[string]any {
	result := make(map[string]any)
	for key, val := range flatenned {
		unflattenJsonHelper(key, val, result)
	}
	return result
}

func extractUnquoted(s string, start int) (string, int) {
	i := start
	for i < len(s) && s[i] != ',' && s[i] != '}' {
		i++
	}
	return strings.TrimSpace(s[start:i]), i - 1
}
