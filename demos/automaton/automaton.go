// Package automaton 实现了 Aho-Corasick 多模式字符串匹配算法，
// 用于在一段文本中同时查找多个关键词，返回所有匹配位置及对应的 TokenID。
package automaton

import "container/list"

// CheckResult 表示一次匹配的结果。
type CheckResult struct {
	StartIndex int // 匹配起始位置（包含）
	EndIndex   int // 匹配结束位置（不包含）
	TokenID    int // 匹配到的关键词 ID，对应 NewAutomaton 传入的切片下标
}

// Automaton 是多模式匹配引擎的接口。
type Automaton interface {
	// Check 在 src 中查找所有已注册的关键词，返回所有匹配结果。
	Check(src []byte) (result []CheckResult)
}

// NewAutomaton 创建一个新的匹配引擎实例。
// words 为待匹配的关键词列表，返回结果中的 TokenID 即为对应关键词在 words 中的下标。
func NewAutomaton(words []string) Automaton {
	wordMap := make(map[int][]byte)
	for i, w := range words {
		wordMap[i] = []byte(w)
	}

	instance := &engine{
		rootNode: newNode(0),
		wordMap:  wordMap,
	}

	instance.buildPrefixTree()
	instance.buildMismatchPointer()

	return instance
}

const defaultTokenID = -1

type node struct {
	id          int
	fail        *node          // 失配指针，指向当前节点对应最长后缀的节点
	nextNodeMap map[byte]*node // 子节点转移表
	tokenID     int            // 该节点对应的关键词 ID，-1 表示非终态
	wordLen     int            // 该节点对应的关键词长度
}

func newNode(id int) *node {
	return &node{
		nextNodeMap: make(map[byte]*node),
		tokenID:     defaultTokenID,
		id:          id,
	}
}

// engine 是 Aho-Corasick 算法的核心实现。
type engine struct {
	rootNode *node
	wordMap  map[int][]byte // tokenID => word
	nodeID   int            // 节点 ID 计数器，避免全局状态
}

// buildPrefixTree 将所有关键词构建成 Trie 前缀树。
func (e *engine) buildPrefixTree() {
	for tokenID, word := range e.wordMap {
		curNode := e.rootNode
		for _, c := range word {
			n, ok := curNode.nextNodeMap[c]
			if !ok {
				e.nodeID++
				n = newNode(e.nodeID)
				curNode.nextNodeMap[c] = n
			}
			curNode = n
		}
		curNode.tokenID = tokenID
		curNode.wordLen = len(word)
	}
}

// buildMismatchPointer 通过 BFS 构建失配指针（failure link）。
// 失配指针的作用是：当某个字符匹配失败时，快速跳转到可能继续匹配的位置。
func (e *engine) buildMismatchPointer() {
	queue := list.New()
	queue.PushFront(e.rootNode)

	for queue.Len() > 0 {
		curNode := queue.Remove(queue.Back()).(*node)
		for c, n := range curNode.nextNodeMap {
			n.fail = e.rootNode
			if curNode != e.rootNode {
				for p := curNode.fail; p != nil; p = p.fail {
					if n1, ok := p.nextNodeMap[c]; ok {
						n.fail = n1
						break
					}
				}
			}
			queue.PushFront(n)
		}
	}
}

// Check 在 src 中查找所有已注册的关键词，返回所有匹配结果。
func (e *engine) Check(src []byte) []CheckResult {
	curNode := e.rootNode
	var result []CheckResult

	for k, v := range src {
		// 沿失配指针回退，直到找到能匹配当前字符的节点或回到根节点
		for curNode.nextNodeMap[v] == nil && curNode != e.rootNode {
			curNode = curNode.fail
		}
		curNode = curNode.nextNodeMap[v]
		if curNode == nil {
			curNode = e.rootNode
		}

		// 匹配成功，记录结果
		if curNode != e.rootNode && curNode.tokenID != defaultTokenID {
			result = append(result, CheckResult{
				StartIndex: k - curNode.wordLen + 1,
				EndIndex:   k + 1,
				TokenID:    curNode.tokenID,
			})
		}
	}
	return result
}
