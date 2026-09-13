// Package avl 实现 AVL 自平衡二叉搜索树
// 任何一个节点的左子树和右子树高度差不超过 1
package avl

// AVL 定义平衡二叉树的公开接口
type AVL interface {
	Set(key int, value interface{})              // 插入或更新键值对
	Del(key int)                                 // 删除指定键
	Get(key int) (value interface{}, ok bool)    // 查找键对应的值
	Print() (keyList []int, valueList []interface{}) // 中序遍历，返回有序键值列表
}

// node 表示树中的一个节点
type node struct {
	left, right *node       // 左右子节点
	key         int         // 键（用于排序）
	height      int         // 当前节点的高度（叶子节点为 1）
	value       interface{} // 存储的值
}

// avl 是 AVL 树的内部实现
type avl struct {
	root *node // 根节点
}

// max 返回两个整数中的较大值
var max = func(a, b int) int {
	if a < b {
		return b
	}
	return a
}

// height 安全地获取节点高度，nil 节点高度为 0
func (a *avl) height(cur *node) int {
	if cur == nil {
		return 0
	}
	return cur.height
}

// leftSpin 左旋转：将右子节点提升为根，原根变为新根的左子节点
/*
       a              c
     /   \          /   \
    b     c   =>   a     h
   / \   / \      / \
  e   f g   h    b   g
                / \
               e   f
*/
func (a *avl) leftSpin(cur *node) (ret *node) {

	ret = cur.right
	cur.right = ret.left
	ret.left = cur

	cur.height = max(a.height(cur.left), a.height(cur.right)) + 1
	ret.height = max(a.height(ret.left), a.height(ret.right)) + 1

	return
}

// rightSpin 右旋转：将左子节点提升为根，原根变为新根的右子节点
/*
       a              b
     /   \          /   \
    b     c   =>   e     a
   / \   / \            / \
  e   f g   h          f   c
                          / \
                         g   h
*/
func (a *avl) rightSpin(cur *node) (ret *node) {

	ret = cur.left
	cur.left = ret.right
	ret.right = cur

	cur.height = max(a.height(cur.left), a.height(cur.right)) + 1
	ret.height = max(a.height(ret.left), a.height(ret.right)) + 1

	return
}

// LL_logic 处理左左情况：右子树高度差为 2，且左子树的左子树更高
// 通过单次右旋恢复平衡
/*
        a                b
      /   \            /   \
     b     c          d      a
    / \              / \    / \
   d   e      =>    f   g  e   c
  / \
 f   g
*/
func (a *avl) LL_logic(cur *node) *node {
	return a.rightSpin(cur)
}

// RR_logic 处理右右情况：右子树高度差为 -2，且右子树的右子树更高
// 通过单次左旋恢复平衡
/*
   a                          c
 /   \                       /  \
b     c         =>          a     e
     / \                   / \   / \
    d   e                 b   d f   g
       / \
      f   g
*/
func (a *avl) RR_logic(cur *node) *node {
	return a.leftSpin(cur)
}

// LR_logic 处理左右情况：左子树高度差为 2，且左子树的右子树更高
// 先对左子树左旋，再对当前节点右旋
/*
        a                  a                   e
      /   \              /   \               /   \
     b     c            e     c             b     a
    / \                / \                 / \   / \
   d   e        =>    b   g         =>    d   f g   c
      / \            / \
     f   g          d   f
*/
func (a *avl) LR_logic(cur *node) *node {
	cur.left = a.leftSpin(cur.left)
	return a.rightSpin(cur)
}

// RL_logic 处理右左情况：右子树高度差为 -2，且右子树的左子树更高
// 先对右子树右旋，再对当前节点左旋
/*
   a                  a                   d
 /   \              /   \               /   \
b     c    =>      b     d      =>     a     c
     / \                / \           / \   / \
    d   e              f   e         b   f g   h
   / \                    / \
  f   g                  g   h
*/
func (a *avl) RL_logic(cur *node) *node {
	cur.right = a.rightSpin(cur.right)
	return a.leftSpin(cur)
}

// checkBalance 检查节点是否失衡，如果失衡则执行相应的旋转操作
// 平衡因子 = 左子树高度 - 右子树高度，绝对值超过 1 时需要旋转
func (a *avl) checkBalance(cur *node) *node {

	if cur == nil {
		return nil
	}

	switch a.height(cur.left) - a.height(cur.right) {
	case 2: // 左子树过高
		if a.height(cur.left.left) > a.height(cur.left.right) {
			// LL 情况：左子树的左子树更高，单次右旋
			cur = a.LL_logic(cur)
		} else {
			// LR 情况：左子树的右子树更高，先左旋再右旋
			cur = a.LR_logic(cur)
		}
	case -2: // 右子树过高
		if a.height(cur.right.left) < a.height(cur.right.right) {
			// RR 情况：右子树的右子树更高，单次左旋
			cur = a.RR_logic(cur)
		} else {
			// RL 情况：右子树的左子树更高，先右旋再左旋
			cur = a.RL_logic(cur)
		}
	default:
		// 平衡因子在 [-1, 1] 范围内，只需更新高度
		cur.height = max(a.height(cur.left), a.height(cur.right)) + 1
	}

	return cur
}

// minNode 查找以 cur 为根的子树中键值最小的节点（最左节点）
func (a *avl) minNode(cur *node) *node {
	for cur.left != nil {
		cur = cur.left
	}
	return cur
}

// maxNode 查找以 cur 为根的子树中键值最大的节点（最右节点）
func (a *avl) maxNode(cur *node) *node {
	for cur.right != nil {
		cur = cur.right
	}
	return cur
}

// insert 递归插入新节点，如果键已存在则更新值
// 插入后检查并修复平衡
func (a *avl) insert(cur *node, new *node) (ret *node) {

	switch {
	case cur == nil: // 找到插入位置
		cur = new
		cur.height = 1
	case cur.key > new.key: // 目标在左子树
		cur.left = a.insert(cur.left, new)
	case cur.key < new.key: // 目标在右子树
		cur.right = a.insert(cur.right, new)
	case cur.key == new.key: // 键已存在，更新值
		cur.value = new.value
	}

	ret = a.checkBalance(cur)
	return
}

// delete 递归删除指定键的节点
// 删除策略：叶子节点直接删除；单子节点用子节点替换；双子节点用前驱或后继替换后递归删除
// 删除后检查并修复平衡
func (a *avl) delete(cur *node, key int) (ret *node) {

	switch {
	case cur == nil: // 键不存在
	case cur.key > key: // 目标在左子树
		cur.left = a.delete(cur.left, key)
	case cur.key < key: // 目标在右子树
		cur.right = a.delete(cur.right, key)
	case cur.key == key: // 找到目标节点
		switch {
		case cur.left == nil && cur.right == nil: // 叶子节点
			cur = nil
		case cur.left != nil && cur.right != nil: // 双子节点
			if a.height(cur.left) < a.height(cur.right) {
				// 右子树更高，用右子树的最小节点替换
				tmpNode := a.minNode(cur.right)
				cur.key = tmpNode.key
				cur.value = tmpNode.value
				cur.right = a.delete(cur.right, tmpNode.key)
			} else {
				// 左子树更高或相等，用左子树的最大节点替换
				tmpNode := a.maxNode(cur.left)
				cur.key = tmpNode.key
				cur.value = tmpNode.value
				cur.left = a.delete(cur.left, tmpNode.key)
			}
		case cur.left != nil: // 只有左子节点
			cur = cur.left
		case cur.right != nil: // 只有右子节点
			cur = cur.right
		}
	}

	ret = a.checkBalance(cur)
	return
}

// get 递归查找指定键的值
func (a *avl) get(cur *node, key int) (value interface{}, ok bool) {
	switch {
	case cur == nil: // 键不存在
		value, ok = nil, false
	case cur.key > key: // 目标在左子树
		value, ok = a.get(cur.left, key)
	case cur.key < key: // 目标在右子树
		value, ok = a.get(cur.right, key)
	case cur.key == key: // 找到目标
		value, ok = cur.value, true
	}
	return
}

// print 中序遍历（左 -> 根 -> 右），返回有序的键值列表
func (a *avl) print(cur *node) (keyList []int, valueList []interface{}) {

	if cur == nil {
		return
	}

	// 遍历左子树
	if cur.left != nil {
		l1, l2 := a.print(cur.left)
		keyList = append(keyList, l1...)
		valueList = append(valueList, l2...)
	}

	// 访问当前节点
	keyList = append(keyList, cur.key)
	valueList = append(valueList, cur.value)

	// 遍历右子树
	if cur.right != nil {
		l1, l2 := a.print(cur.right)
		keyList = append(keyList, l1...)
		valueList = append(valueList, l2...)
	}
	return
}

// GenAVL 创建并返回一个新的 AVL 树实例
func GenAVL() AVL {
	return &avl{}
}

// Set 插入或更新键值对
func (a *avl) Set(key int, value interface{}) {

	a.root = a.insert(a.root, &node{
		key:   key,
		value: value,
	})
}

// Del 删除指定键
func (a *avl) Del(key int) {
	a.root = a.delete(a.root, key)
}

// Get 查找键对应的值
func (a *avl) Get(key int) (value interface{}, ok bool) {
	return a.get(a.root, key)
}

// Print 中序遍历，返回有序的键值列表
func (a *avl) Print() (keyList []int, valueList []interface{}) {
	return a.print(a.root)
}
