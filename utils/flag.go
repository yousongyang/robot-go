package atsf4g_go_robot_util

import (
	"flag"
	"strconv"
	"strings"
)

// StringSliceFlag 支持多次指定的 flag（如 --set KEY=VAL --set FOO=BAR）
type StringSliceFlag []string

func (s *StringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *StringSliceFlag) Set(val string) error {
	*s = append(*s, val)
	return nil
}

func GetFlagString(fs *flag.FlagSet, name string) string {
	f := fs.Lookup(name)
	if f == nil {
		return ""
	}
	return f.Value.String()
}

func GetFlagInt32(fs *flag.FlagSet, name string) int32 {
	str := GetFlagString(fs, name)
	if str == "" {
		return 0
	}
	var v int32
	value, err := strconv.Atoi(str)
	if err != nil {
		return 0
	}
	v = int32(value)
	return v
}

// ParseSetFlags 将 ["KEY=VALUE", ...] 格式的字符串切片解析为 map。
func ParseSetFlags(sets []string) map[string]string {
	vars := make(map[string]string, len(sets))
	for _, s := range sets {
		if idx := strings.IndexByte(s, '='); idx > 0 {
			vars[s[:idx]] = s[idx+1:]
		}
	}
	return vars
}

func ParseSliceFlags(fs *flag.FlagSet, name string) []string {
	f := fs.Lookup(name)
	if f == nil {
		return nil
	}
	if sv, ok := f.Value.(*StringSliceFlag); ok {
		return []string(*sv)
	}
	return nil
}

// GetSetVars 从已解析的 FlagSet 中提取 --set KEY=VALUE 变量并返回 map。
func GetSetVars(fs *flag.FlagSet) map[string]string {
	f := fs.Lookup("set")
	if f == nil {
		return nil
	}
	if sv, ok := f.Value.(*StringSliceFlag); ok {
		return ParseSetFlags([]string(*sv))
	}
	return nil
}
