package launcher

type Builder interface {
	// Build 构建启动器
	Build() (Launcher, error)
}
