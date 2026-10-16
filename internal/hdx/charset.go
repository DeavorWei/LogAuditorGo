package hdx

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

var gbkEncoding encoding.Encoding

func init() {
	var err error
	gbkEncoding, err = htmlindex.Get("gb18030")
	if err != nil {
		gbkEncoding = simplifiedchinese.GB18030
	}
}

// DecodeGBK 将 GBK/GB2312/GB18030 编码的字节数组转为 UTF-8 字符串
// 遇非法或截断字节使用 \uFFFD 容错替代，避免整体报错乱码
func DecodeGBK(src []byte) (string, error) {
	if len(src) == 0 {
		return "", nil
	}
	if utf8.Valid(src) {
		return string(src), nil
	}
	reader := transform.NewReader(bytes.NewReader(src), gbkEncoding.NewDecoder())
	d, err := io.ReadAll(reader)
	if err != nil {
		return string(src), err
	}
	return string(d), nil
}

// DecodeGBKBytes 将 GBK/GB2312/GB18030 字节转为 UTF-8 字节
// 遇非法或截断字节使用 \uFFFD 容错替代，避免整体报错乱码
func DecodeGBKBytes(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if utf8.Valid(src) {
		return src, nil
	}
	reader := transform.NewReader(bytes.NewReader(src), gbkEncoding.NewDecoder())
	d, err := io.ReadAll(reader)
	if err != nil {
		return src, err
	}
	return d, nil
}

// XMLCharsetReader 为 xml.Decoder 提供 GB2312/GBK/GB18030 字符集转码
func XMLCharsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "gbk", "gb2312", "gb18030", "windows-936", "cp936":
		return transform.NewReader(input, gbkEncoding.NewDecoder()), nil
	case "utf-8", "utf8", "":
		return input, nil
	default:
		return nil, fmt.Errorf("unsupported xml charset: %s", charset)
	}
}
