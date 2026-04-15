package dsa

import "strings"

type HTTPMethod int

const (
	GET HTTPMethod = iota
	POST
	PUT
	PATCH
	DELETE
)

type Handler interface {
	handle()
}

type TrieNode struct {
	children      map[string]*TrieNode
	paramChild    *TrieNode
	wildCardChild *TrieNode
	paramName     string
	handler       map[HTTPMethod]Handler
}

func NewTrieNode() *TrieNode {
	return &TrieNode{
		children: make(map[string]*TrieNode),
		handler:  make(map[HTTPMethod]Handler),
	}
}

type Router struct {
	root *TrieNode
}

func NewRouter() *Router {
	return &Router{
		root: &TrieNode{
			children: make(map[string]*TrieNode, 0),
		},
	}
}

func (r *Router) findHandler(method HTTPMethod, path string) (Handler, map[string]string) {

	node := r.root
	parts := strings.Split(path, "/")
	params := make(map[string]string)
	for _, part := range parts {
		if _, ok := node.children[part]; ok {
			node = node.children[part]
		} else if node.paramChild != nil {
			node = node.paramChild
			params[node.paramName] = part
		} else if node.wildCardChild != nil {
			return node.wildCardChild.handler[method], nil
		}
	}
	return node.handler[method], params
}

func (r *Router) add(method HTTPMethod, path string, handler Handler) {
	node := r.root
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if r.isParam(part) {
			paramChild := node.paramChild
			if paramChild == nil {
				paramChild = NewTrieNode()
			}
			paramChild.paramName = part
			node.paramChild = paramChild
			node = paramChild
		} else if r.isWildChard(part) {
			node.wildCardChild = NewTrieNode()
			node = node.wildCardChild
		} else {
			if node.children[part] == nil {
				node.children[part] = NewTrieNode()
			}
			node = node.children[part]
		}
	}
	node.handler[method] = handler
}

func (r *Router) isParam(part string) bool {
	return strings.HasPrefix(part, ":")
}

func (r *Router) isWildChard(part string) bool {
	return strings.HasPrefix(part, "*")
}
