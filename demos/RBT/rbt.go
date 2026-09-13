// Package rbt 实现了红黑树数据结构
// 红黑树是一种自平衡二叉搜索树，保证 O(log n) 的查找、插入和删除操作
package rbt

// RBT 定义了红黑树的公共接口
type RBT interface {
	Set(key int, value interface{})          // 插入或更新键值对
	Del(key int)                             // 删除指定键
	Get(key int) (value interface{}, ok bool) // 查找指定键的值
	Print() (keyList []int, valueList []interface{}) // 中序遍历返回有序键值对
}

const (
	RED   = true  // 红色节点
	BLACK = false // 黑色节点
)

// node 表示红黑树的节点
type node struct {
	parent, right, left *node
	color               bool        // 节点颜色：RED 或 BLACK
	key                 int         // 键（仅支持整数）
	value               interface{} // 值（任意类型）
}

// rbt 是红黑树的具体实现
type rbt struct {
	root *node // 根节点
	null *node // 哨兵节点（代替 nil，简化边界处理）
	len  int   // 节点数量
}

// 左旋操作：将 x 的右子节点 y 旋转为 x 的父节点
// 保持二叉搜索树性质：x < y.left < y
func (r *rbt) leftRotate(x *node) {
	y := x.right       // y 是 x 的右子节点
	x.right = y.left   // 将 y 的左子树挂到 x 的右边
	if y.left != r.null {
		y.left.parent = x
	}
	y.parent = x.parent // y 接管 x 的父节点
	switch {
	case x.parent == r.null:
		r.root = y
	case x == x.parent.left:
		x.parent.left = y
	default:
		x.parent.right = y
	}

	y.left = x   // x 成为 y 的左子节点
	x.parent = y
}

// 右旋操作：将 y 的左子节点 x 旋转为 y 的父节点
// 保持二叉搜索树性质：x < y.right < y
func (r *rbt) rightRotate(y *node) {
	x := y.left       // x 是 y 的左子节点
	y.left = x.right  // 将 x 的右子树挂到 y 的左边
	if x.right != r.null {
		x.right.parent = y
	}
	x.parent = y.parent // x 接管 y 的父节点
	switch {
	case y.parent == r.null:
		r.root = x
	case y == y.parent.left:
		y.parent.left = x
	default:
		y.parent.right = x
	}
	x.right = y   // y 成为 x 的右子节点
	y.parent = x
}

// insert 将新节点 z 插入到红黑树中
// 如果键已存在，则更新其值
func (r *rbt) insert(z *node) {
	y := r.null  // y 记录 x 的父节点
	x := r.root

	// 标准 BST 插入：找到合适的插入位置
	for x != r.null {
		y = x
		switch {
		case z.key < x.key:
			x = x.left
		case z.key > x.key:
			x = x.right
		case z.key == x.key:
			x.value = z.value // 键已存在，更新值并返回
			return
		}
	}

	// 设置新节点的父节点
	z.parent = y
	switch {
	case y == r.null:
		r.root = z // 树为空，新节点成为根节点
	case z.key < y.key:
		y.left = z
	default:
		y.right = z
	}

	// 初始化新节点：左右子节点为哨兵，颜色为红色
	z.left = r.null
	z.right = r.null
	z.color = RED

	r.insertFixUp(z) // 修复红黑树性质
	r.len++
}

// insertFixUp 修复插入操作后可能被破坏的红黑树性质
// 新插入的节点为红色，可能导致连续红色节点（性质4被破坏）
func (r *rbt) insertFixUp(z *node) {
	for z.parent.color == RED {
		if z.parent == z.parent.parent.left {
			// 父节点是祖父节点的左子节点
			y := z.parent.parent.right // 叔节点（祖父节点的右子节点）
			if y.color == RED {
				// 情况1：叔节点为红色
				// 解决方案：父节点和叔节点变黑，祖父节点变红，z 上移两层
				y.color = BLACK
				z.parent.color = BLACK
				z.parent.parent.color = RED
				z = z.parent.parent
			} else {
				if z == z.parent.right {
					// 情况2：叔节点为黑色 & z 是右子节点
					// 解决方案：左旋转，转化为情况3
					z = z.parent
					r.leftRotate(z)
				}
				// 情况3：叔节点为黑色 & z 是左子节点
				// 解决方案：父节点变黑，祖父节点变红，右旋祖父节点
				z.parent.color = BLACK
				z.parent.parent.color = RED
				r.rightRotate(z.parent.parent)
			}
		} else {
			// 父节点是祖父节点的右子节点（镜像情况）
			y := z.parent.parent.left // 叔节点（祖父节点的左子节点）
			if y.color == RED {
				// 情况1：叔节点为红色
				y.color = BLACK
				z.parent.color = BLACK
				z.parent.parent.color = RED
				z = z.parent.parent
			} else {
				if z == z.parent.left {
					// 情况2：叔节点为黑色 & z 是左子节点
					z = z.parent
					r.rightRotate(z)
				}
				// 情况3：叔节点为黑色 & z 是右子节点
				z.parent.color = BLACK
				z.parent.parent.color = RED
				r.leftRotate(z.parent.parent)
			}
		}
	}
	r.root.color = BLACK // 确保根节点为黑色（性质2）
}

// transplant 用节点 v 替换节点 u 在树中的位置
// 用于删除操作中替换被删除的节点
func (r *rbt) transplant(u, v *node) {
	switch {
	case u.parent == r.null:
		r.root = v
	case u == u.parent.left:
		u.parent.left = v
	default:
		u.parent.right = v
	}
	v.parent = u.parent
}

// minimum 返回以 x 为根的子树中的最小节点（最左边的节点）
func (r *rbt) minimum(x *node) *node {
	for x.left != r.null {
		x = x.left
	}
	return x
}

// deleteFixUp 修复删除操作后可能被破坏的红黑树性质
// 当删除的节点为黑色时，需要修复黑色高度平衡
func (r *rbt) deleteFixUp(x *node) {
	for x != r.root && x.color == BLACK {
		if x == x.parent.left {
			// x 是左子节点
			w := x.parent.right // w 是 x 的兄弟节点

			if w.color == RED {
				// 情况1：兄弟节点 w 为红色
				// 解决方案：w 变黑，父节点变红，左旋父节点
				w.color = BLACK
				x.parent.color = RED
				r.leftRotate(x.parent)
				w = x.parent.right
			}

			if w.left.color == BLACK && w.right.color == BLACK {
				// 情况2：兄弟节点 w 为黑色，w 的两个子节点都为黑色
				// 解决方案：w 变红，x 上移一层
				w.color = RED
				x = x.parent
			} else {
				if w.right.color == BLACK {
					// 情况3：兄弟节点 w 为黑色，w 的右子节点为黑色，左子节点为红色
					// 解决方案：w 的左子节点变黑，w 变红，右旋 w
					w.left.color = BLACK
					w.color = RED
					r.rightRotate(w)
					w = x.parent.right
				}

				// 情况4：兄弟节点 w 为黑色，w 的右子节点为红色
				// 解决方案：w 颜色设为父节点颜色，父节点变黑，w 的右子节点变黑，左旋父节点
				w.color = x.parent.color
				x.parent.color = BLACK
				w.right.color = BLACK
				r.leftRotate(x.parent)
				x = r.root
			}
		} else {
			// x 是右子节点（镜像情况）
			w := x.parent.left // w 是 x 的兄弟节点

			if w.color == RED {
				// 情况1：兄弟节点 w 为红色
				w.color = BLACK
				x.parent.color = RED
				r.rightRotate(x.parent)
				w = x.parent.left
			}

			if w.left.color == BLACK && w.right.color == BLACK {
				// 情况2：兄弟节点 w 为黑色，w 的两个子节点都为黑色
				w.color = RED
				x = x.parent
			} else {
				if w.left.color == BLACK {
					// 情况3：兄弟节点 w 为黑色，w 的左子节点为黑色，右子节点为红色
					w.right.color = BLACK
					w.color = RED
					r.leftRotate(w)
					w = x.parent.left
				}

				// 情况4：兄弟节点 w 为黑色，w 的左子节点为红色
				w.color = x.parent.color
				x.parent.color = BLACK
				w.left.color = BLACK
				r.rightRotate(x.parent)
				x = r.root
			}
		}
	}
	x.color = BLACK // 确保 x 为黑色
}

// delete 从红黑树中删除节点 z
func (r *rbt) delete(z *node) {
	y := z           // y 记录实际被删除或移动的节点
	x := r.null      // x 记录需要修复的节点
	yOriginalColor := y.color

	switch {
	case z.left == r.null:
		// 情况1：z 没有左子节点，用右子节点替换 z
		x = z.right
		r.transplant(z, z.right)
	case z.right == r.null:
		// 情况2：z 没有右子节点，用左子节点替换 z
		x = z.left
		r.transplant(z, z.left)
	default:
		// 情况3：z 有两个子节点，用 z 的后继（右子树的最小值）替换 z
		y = r.minimum(z.right) // 找到 z 的后继节点
		yOriginalColor = y.color
		x = y.right

		if y.parent == z {
			// 后继节点就是 z 的右子节点
			x.parent = y // 修复哨兵节点的父指针
		} else {
			// 后继节点不是 z 的右子节点
			r.transplant(y, y.right) // 用 y 的右子节点替换 y
			y.right = z.right        // y 接管 z 的右子树
			y.right.parent = y
		}

		r.transplant(z, y) // 用 y 替换 z
		y.left = z.left    // y 接管 z 的左子树
		y.left.parent = y
		y.color = z.color  // 保持 z 的颜色
	}

	// 如果删除的节点是黑色，需要修复红黑树性质
	if yOriginalColor == BLACK {
		r.deleteFixUp(x)
	}

	r.len--
}

// get 在红黑树中查找指定键的节点
// 如果找到返回该节点，否则返回哨兵节点
func (r *rbt) get(key int) (ret *node) {
	ret = r.null
	x := r.root

	for x != r.null {
		switch {
		case key < x.key:
			x = x.left
		case key > x.key:
			x = x.right
		case key == x.key:
			ret = x
			x = r.null // 找到目标节点，结束循环
		}
	}

	return
}

// set 创建新节点并插入到红黑树中
func (r *rbt) set(key int, value interface{}) {
	r.insert(&node{
		key:   key,
		value: value,
	})
}

// del 删除指定键的节点
func (r *rbt) del(key int) {
	x := r.get(key)
	if x == r.null {
		return // 键不存在，直接返回
	}

	r.delete(x)
}

// print 使用迭代中序遍历返回有序的键值对列表
// 使用显式栈避免递归调用
func (r *rbt) print() (keyList []int, valueList []interface{}) {
	stack := make([]*node, r.len, r.len) // 显式栈
	stackIndex := -1
	x := r.root

	for x != r.null || stackIndex > -1 {
		// 一直向左走，将沿途节点压入栈
		for x != r.null {
			stackIndex++
			stack[stackIndex] = x
			x = x.left
		}

		// 弹出栈顶节点，访问它，然后转向右子树
		if stackIndex > -1 {
			x = stack[stackIndex]
			stackIndex--

			keyList = append(keyList, x.key)
			valueList = append(valueList, x.value)

			x = x.right
		}
	}

	return
}

// GenRBT 创建并返回一个新的空红黑树
func GenRBT() RBT {
	null := &node{color: BLACK} // 创建哨兵节点（黑色）
	return &rbt{
		root: null, // 根节点初始指向哨兵
		null: null, // 哨兵节点
	}
}

// Get 查找指定键的值
// 返回值和是否找到的标志
func (r *rbt) Get(key int) (value interface{}, ok bool) {
	if x := r.get(key); x != r.null {
		value, ok = x.value, true
	}
	return
}

// Set 插入或更新键值对
func (r *rbt) Set(key int, value interface{}) {
	r.set(key, value)
}

// Del 删除指定键
func (r *rbt) Del(key int) {
	r.del(key)
}

// Print 返回有序的键值对列表（中序遍历）
func (r *rbt) Print() (keyList []int, valueList []interface{}) {
	return r.print()
}
