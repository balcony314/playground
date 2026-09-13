package bt

// BT B树接口
type BT interface {
	Set(key int, value interface{})                 // 插入或更新键值对
	Del(key int)                                    // 删除指定键
	Get(key int) (value interface{}, ok bool)       // 查找键，返回值和是否找到
	Print() (keyList []int, valueList []interface{}) // 中序遍历，返回有序键值切片
}

// node B树节点
type node struct {
	n int    // 关键字个数
	leaf bool // 是否为叶子节点

	c     []*node       // 子节点指针，t <= len(c) <= 2t
	key   []int         // 关键字，t-1 <= len(key) <= 2t-1
	value []interface{} // 关键字对应的值
}

// bTree B树实现
type bTree struct {
	root *node // 根节点
	t    int   // 最小度数，t >= 2
	// B树性质：
	// a. 除根节点外，每个节点至少有 t-1 个关键字、t 个孩子
	// b. 每个节点至多有 2t-1 个关键字、2t 个孩子
}

// search 在以x为根的子树中查找key，返回节点和索引
func (b *bTree) search(x *node, key int) (*node, int) {
	i := 0
	for i < x.n && key > x.key[i] {
		i++
	}

	if i < x.n && key == x.key[i] {
		return x, i
	} else if x.leaf {
		return nil, 0
	} else {
		//Disk-Read(x,c[i])
		k := x.c[i]
		return b.search(k, key)
	}
}

// splitChild 将x的第i个满子节点y分裂为两个节点
func (b *bTree) splitChild(x *node, i int) {
	y := x.c[i]   // 要分裂的节点
	y.n = b.t - 1 // 2t-1 => t-1

	z := &node{
		n:     y.n,
		key:   make([]int, 2*b.t-1),
		value: make([]interface{}, 2*b.t-1),
		c:     make([]*node, 2*b.t),
		leaf:  y.leaf,
	} //分裂出来的节点

	//copy y -> z
	for j := 0; j < b.t-1; j++ {
		z.key[j] = y.key[j+b.t]
		z.value[j] = y.value[j+b.t]
	}

	if !y.leaf {
		//不是叶子节点
		for j := 0; j < b.t; j++ {
			z.c[j] = y.c[j+b.t]
		}
	}

	//x节点空一个位置供y节点上升
	for j := x.n; j > i; j-- {
		x.c[j+1] = x.c[j]
	}

	x.c[i+1] = z
	for j := x.n - 1; j >= i; j-- {
		x.key[j+1] = x.key[j]
		x.value[j+1] = x.value[j]
	}
	x.key[i] = y.key[b.t-1]
	x.value[i] = y.value[b.t-1]

	x.n++

	//Disk-Write(x)
	//Disk-Write(y)
	//Disk-Write(z)
}

// insert 插入键值对，根节点满时先分裂再插入
func (b *bTree) insert(k int, v interface{}) {
	r := b.root
	if r.n == 2*b.t-1 {
		// 根节点已满，创建新根并分裂
		s := &node{
			leaf:  false,
			n:     0,
			key:   make([]int, 2*b.t-1),
			value: make([]interface{}, 2*b.t-1),
			c:     make([]*node, 2*b.t),
		}
		b.root = s
		s.c[0] = r

		b.splitChild(s, 0)
		//Disk-read(s)
		b.insertNonfull(s, k, v)
	} else {
		b.insertNonfull(r, k, v)
	}
}

// insertNonfull 在非满节点x中插入键值对
func (b *bTree) insertNonfull(x *node, k int, v interface{}) {
	i := x.n - 1

	if x.leaf {
		// 叶子节点：直接插入，移动元素腾出位置
		for i >= 0 && k < x.key[i] {
			x.key[i+1] = x.key[i]
			i--
		}
		i++

		x.key[i] = k
		x.value[i] = v
		x.n++
	} else {
		// 内部节点：找到合适的子节点
		for i >= 0 && k < x.key[i] {
			i--
		}
		i++

		//Disk-Read(x.c[i])

		if x.c[i].n == 2*b.t-1 {
			// 子节点已满，先分裂
			b.splitChild(x, i)
			//Disk-read(x)
			if k > x.key[i] {
				i++
			}
		}

		b.insertNonfull(x.c[i], k, v)
	}
}

// mergeChild 合并x的第i个关键字和相邻子节点y、z（y.key < x.key[i] < z.key）
// 前提：y.n == t-1 && z.n == t-1
func (b *bTree) mergeChild(x *node, i int, y *node, z *node) {
	// 将z的所有关键字和x的第i个关键字合并到y
	for j := b.t; j < 2*b.t-1; j++ {
		y.key[j] = z.key[j-b.t]
		y.value[j] = z.value[j-b.t]
	}
	y.key[b.t-1] = x.key[i]
	y.value[b.t-1] = x.value[i]

	if !y.leaf {
		for j := b.t; j < 2*b.t; j++ {
			y.c[j] = z.c[j-b.t]
		}
	}
	y.n = 2*b.t - 1

	// 从x中删除第i个关键字和指向z的指针
	for j := i; j+1 < x.n; j++ {
		x.key[j] = x.key[j+1]
		x.value[j] = x.value[j+1]
	}

	for j := i + 1; j+1 < x.n+1; j++ {
		x.c[j] = x.c[j+1]
	}
	x.c[x.n] = nil
	x.n--

	//Disk-Write(x)
	//Disk-Write(y)
	//Disk-Write(z)
}

// borrowKey 节点k向兄弟节点借用一个关键字
// 参数：x-父节点, i-k在x中的索引, y-k的前驱兄弟, k-当前节点, z-k的后继兄弟
// 返回：是否成功借用
func (b *bTree) borrowKey(x *node, i int, y *node, k *node, z *node) bool {
	// 尝试从前驱兄弟y借用
	if y != nil && y.n >= b.t {
		// k中所有元素右移，空出位置
		for j := k.n - 1; j >= 0; j-- {
			k.key[j+1] = k.key[j]
			k.value[j+1] = k.value[j]
		}
		k.key[0] = x.key[i-1]
		k.value[0] = x.value[i-1]

		for j := k.n; j >= 0; j-- {
			k.c[j+1] = k.c[j]
		}
		k.c[0] = y.c[y.n]
		k.n++

		// x的关键字下降，y的最大关键字上升
		x.key[i-1] = y.key[y.n-1]
		x.value[i-1] = y.value[y.n-1]

		y.n--

		//Disk-Write(x)
		//Disk-Write(k)
		//Disk-Write(y)

		return true
	}

	// 尝试从后继兄弟z借用
	if z != nil && z.n >= b.t {
		// k的最大关键字位置放入x的关键字
		k.key[k.n] = x.key[i]
		k.value[k.n] = x.value[i]
		k.c[k.n+1] = z.c[0]
		k.n++

		// z的最小关键字上升到x
		x.key[i] = z.key[0]
		x.value[i] = z.value[0]

		// z中所有元素左移
		for j := 1; j < z.n; j++ {
			z.key[j-1] = z.key[j]
			z.value[j-1] = z.value[j]
		}
		for j := 1; j < z.n+1; j++ {
			z.c[j-1] = z.c[j]
		}
		z.c[z.n] = nil
		z.n--

		//Disk-Write(x)
		//Disk-Write(k)
		//Disk-Write(z)

		return true
	}

	return false
}

// deleteNonOne 在节点x中删除key（x至少有2个关键字）
func (b *bTree) deleteNonOne(x *node, key int) {
	if x.leaf {
		// 叶子节点：直接删除
		i := 0
		for i < x.n && x.key[i] < key {
			i++
		}

		if i == x.n || x.key[i] != key {
			return
		}

		// 左移覆盖被删除元素
		for j := i; j+1 < x.n; j++ {
			x.key[j] = x.key[j+1]
			x.value[j] = x.value[j+1]
		}

		x.n--
		//Disk-write(x)
	} else {
		// 内部节点
		i := 0
		for i < x.n && x.key[i] < key {
			i++
		}

		if i < x.n && key == x.key[i] {
			// 找到key，用前驱或后继替换后递归删除
			y := x.c[i]
			//Disk-read(y)
			z := x.c[i+1]
			//Disk-read(z)

			switch {
			case y.n >= b.t:
				// 用前驱替换
				tmpNode, tmpIndex := y, y.n-1
				x.key[i] = tmpNode.key[tmpIndex]
				x.value[i] = tmpNode.value[tmpIndex]
				b.deleteNonOne(y, tmpNode.key[tmpIndex])
			case z.n >= b.t:
				// 用后继替换
				tmpNode, tmpIndex := z, 0
				x.key[i] = tmpNode.key[tmpIndex]
				x.value[i] = tmpNode.value[tmpIndex]
				b.deleteNonOne(z, tmpNode.key[tmpIndex])
			default:
				// y和z都是t-1，合并后递归删除
				b.mergeChild(x, i, y, z)
				//Disk-read(y)
				b.deleteNonOne(y, key)
			}
		} else {
			// key在子树中，确保子节点关键字 >= t
			k := x.c[i]
			//Disk-read(k)

			var y, z *node
			if i != 0 {
				y = x.c[i-1]
				//Disk-read(y)
			}

			if i != x.n {
				z = x.c[i+1]
				//Disk-read(z)
			}

			switch {
			case k.n >= b.t:
				b.deleteNonOne(k, key)
			case b.borrowKey(x, i, y, k, z):
				// 从兄弟借用成功
				b.deleteNonOne(k, key)
			default:
				// 无法借用，合并
				if z != nil {
					b.mergeChild(x, i, k, z)
					//Disk-read(k)
					b.deleteNonOne(k, key)
				} else {
					b.mergeChild(x, i-1, y, k)
					//Disk-read(y)
					b.deleteNonOne(y, key)
				}
			}
		}
	}
}

// delete 删除键k
func (b *bTree) delete(k int) {
	if b.root.n == 1 {
		// 根节点只有1个关键字，需要特殊处理
		y := b.root.c[0]
		//Disk-Read(y)
		z := b.root.c[1]
		//Disk-Read(z)

		switch {
		case b.root.leaf:
			b.deleteNonOne(b.root, k)
		case y.n == b.t-1 && z.n == b.t-1:
			// 两个子节点都是t-1，合并后降低树高
			b.mergeChild(b.root, 0, y, z)
			//Disk-Read(y)
			b.root = y
			b.deleteNonOne(y, k)
		default:
			b.deleteNonOne(b.root, k)
		}

	} else {
		b.deleteNonOne(b.root, k)
	}
}

// print 中序遍历以x为根的子树，返回有序键值切片
func (b *bTree) print(x *node) (keyList []int, valueList []interface{}) {
	if x.leaf {
		keyList = append(keyList, x.key[0:x.n]...)
		valueList = append(valueList, x.value[0:x.n]...)
	} else {
		for i := 0; i < x.n; i++ {
			key, value := b.print(x.c[i])
			keyList = append(keyList, key...)
			valueList = append(valueList, value...)

			keyList = append(keyList, x.key[i])
			valueList = append(valueList, x.value[i])
		}

		key, value := b.print(x.c[x.n])
		keyList = append(keyList, key...)
		valueList = append(valueList, value...)
	}

	return
}

// GenBT 创建B树实例，t为最小度数（t >= 2）
func GenBT(t int) BT {
	return &bTree{
		t: t,
		root: &node{
			leaf:  true,
			c:     make([]*node, 2*t),
			key:   make([]int, 2*t-1),
			value: make([]interface{}, 2*t-1),
		},
	}
}

// Set 插入或更新键值对
func (b *bTree) Set(key int, value interface{}) {
	b.insert(key, value)
}

// Del 删除指定键
func (b *bTree) Del(key int) {
	b.delete(key)
}

// Get 查找键对应的值
func (b *bTree) Get(key int) (value interface{}, ok bool) {
	if node, index := b.search(b.root, key); node != nil {
		value, ok = node.value[index], true
	}
	return
}

// Print 中序遍历，返回有序键值切片
func (b *bTree) Print() (keyList []int, valueList []interface{}) {
	keyList, valueList = b.print(b.root)
	return
}
