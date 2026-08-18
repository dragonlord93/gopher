package main

import (
	"fmt"
	"strings"
)

type TrieNode struct {
	val      string
	isDir    bool
	children map[string]*TrieNode
}
type FileSystem struct {
	root *TrieNode
}

func NewFileSystem() *FileSystem {
	return &FileSystem{
		root: &TrieNode{
			children: make(map[string]*TrieNode, 0),
		},
	}
}

func (fs *FileSystem) addFile(path string) string {
	root := fs.root
	pTokens := strings.Split(path, "/")

	dirs, leaf := pTokens[:len(pTokens)-1], pTokens[len(pTokens)-1]
	for _, token := range dirs {
		if _, ok := root.children[token]; !ok {
			if len(root.children) >= 5 {
				return ""
			}
			root.children[token] = &TrieNode{val: token, children: make(map[string]*TrieNode, 0)}
		}
		root = root.children[token]
	}

	final := leaf
	if _, ok := root.children[leaf]; ok {
		final = nextFreeName(root, leaf)
	}
	if len(root.children) >= 5 {
		return ""
	}
	root.children[final] = &TrieNode{val: final}
	return path
}

func splitExt(name string) (base, ext string) {
	i := strings.LastIndex(name, ".")
	if i <= 0 { // no dot at all, or a dotfile like ".gitignore"
		return name, ""
	}
	return name[:i], name[i:]
}

func nextFreeName(dir *TrieNode, leaf string) string {
	base, ext := splitExt(leaf)
	for k := 1; ; k++ {
		cand := fmt.Sprintf("%s(%d)%s", base, k, ext)
		if _, taken := dir.children[cand]; !taken {
			return cand
		}
	}
}

func (fs *FileSystem) get(path string) []string {

	root := fs.root
	pTokens := strings.Split(path, "/")

	for _, token := range pTokens {
		if _, ok := root.children[token]; !ok {
			return nil
		}
		root = root.children[token]
	}
	paths := make([]string, 0)
	for k, _ := range root.children {
		paths = append(paths, k)
	}

	return paths
}

func main() {
	fs := NewFileSystem()
	fs.addFile("/a/b/c/d.txt")
	fs.addFile("/a/b/c/d.txt")
	fs.addFile("/a/b/c/d/e/d.txt")
	fmt.Println(fs.get("/a/b/c"))
	fmt.Println(fs.get("/a/b/c/d/e"))
}
