package shadow

func len(x int) int { return x }

func use() {
	len(3)
}
