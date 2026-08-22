package hdx

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// DecodeGBK 将 GBK/GB2312/GB18030 编码的字节数组转为 UTF-8 字符串
func DecodeGBK(src []byte) (string, error) {
	if len(src) == 0 {
		return "", nil
	}
	if utf8.Valid(src) {
		return string(src), nil
	}
	reader := transform.NewReader(bytes.NewReader(src), simplifiedchinese.GB18030.NewDecoder())
	d, err := io.ReadAll(reader)
	if err != nil {
		return string(src), err
	}
	return string(d), nil
}

// DecodeGBKBytes 将 GBK/GB2312/GB18030 字节转为 UTF-8 字节
func DecodeGBKBytes(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if utf8.Valid(src) {
		return src, nil
	}
	reader := transform.NewReader(bytes.NewReader(src), simplifiedchinese.GB18030.NewDecoder())
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
		return transform.NewReader(input, simplifiedchinese.GB18030.NewDecoder()), nil
	case "utf-8", "utf8", "":
		return input, nil
	default:
		return nil, fmt.Errorf("unsupported xml charset: %s", charset)
	}
}
