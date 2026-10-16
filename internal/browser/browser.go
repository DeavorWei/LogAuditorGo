package browser

// Open 调用系统默认浏览器访问指定网页 URL
func Open(url string) error {
	return openURL(url)
}
