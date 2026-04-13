package atsf4g_go_robot_case

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// CaseFileMode 标识 case 文件的解析模式
type CaseFileMode int

const (
	// CaseFileModeStandalone 单机交互模式（CLI RunCaseFileStandAlone）
	// 压测行格式: CaseName OpenIDPrefix UserCount TargetQPS UserBatchCount RunTime [args...]
	// ErrorBreak 固定为 true，OpenIDStart 固定为 0
	CaseFileModeStandalone CaseFileMode = iota
	// CaseFileModeDistributed Solo / Master 分布式模式
	// 压测行格式: CaseName ErrorBreak OpenIDPrefix IDStart IDEnd TargetQPS RunTime [args...]
	CaseFileModeDistributed
)

// ParseDistributedLine 解析分布式模式的一行压测参数（CaseFileModeDistributed）
// 格式: CaseName ErrorBreak OpenIDPrefix IDStart IDEnd TargetQPS RunTime [args...]
func ParseDistributedLine(cmd []string) (Params, error) {
	if len(cmd) < 7 {
		return Params{}, fmt.Errorf("Args Error: need at least 7 args (CaseName ErrorBreak OpenIDPrefix IDStart IDEnd TargetQPS RunTime)")
	}
	errorBreak := strings.ToLower(cmd[1]) == "true" || cmd[1] == "1"
	idStart, err := strconv.ParseInt(cmd[3], 10, 64)
	if err != nil {
		return Params{}, fmt.Errorf("idStart parse error: %w", err)
	}
	idEnd, err := strconv.ParseInt(cmd[4], 10, 64)
	if err != nil {
		return Params{}, fmt.Errorf("idEnd parse error: %w", err)
	}
	targetQPS, err := strconv.ParseFloat(cmd[5], 64)
	if err != nil {
		return Params{}, fmt.Errorf("targetQPS parse error: %w", err)
	}
	runTime, err := strconv.ParseInt(cmd[6], 10, 64)
	if err != nil {
		return Params{}, fmt.Errorf("runTime parse error: %w", err)
	}
	params := Params{
		CaseName:     cmd[0],
		ErrorBreak:   errorBreak,
		OpenIDPrefix: cmd[2],
		OpenIDStart:  idStart,
		OpenIDEnd:    idEnd,
		TargetQPS:    targetQPS,
		RunTime:      runTime,
	}
	if len(cmd) > 7 {
		params.ExtraArgs = cmd[7:]
	}
	return params, nil
}

// ParseStandaloneLine 解析单机模式的一行压测参数（CaseFileModeStandalone）
// 格式: CaseName OpenIDPrefix UserCount TargetQPS UserBatchCount RunTime [args...]
// ErrorBreak 固定为 true，OpenIDStart 固定为 0
func ParseStandaloneLine(cmd []string) (Params, error) {
	if len(cmd) < 6 {
		return Params{}, fmt.Errorf("Args Error: need at least 6 args (CaseName OpenIDPrefix UserCount TargetQPS UserBatchCount RunTime)")
	}
	userCount, err := strconv.ParseInt(cmd[2], 10, 64)
	if err != nil {
		return Params{}, fmt.Errorf("userCount parse error: %w", err)
	}
	if userCount <= 0 {
		return Params{}, fmt.Errorf("userCount must be greater than 0")
	}
	targetQPS, err := strconv.ParseFloat(cmd[3], 64)
	if err != nil {
		return Params{}, fmt.Errorf("targetQPS parse error: %w", err)
	}
	userBatchCount, err := strconv.ParseInt(cmd[4], 10, 64)
	if err != nil {
		return Params{}, fmt.Errorf("userBatchCount parse error: %w", err)
	}
	runTime, err := strconv.ParseInt(cmd[5], 10, 64)
	if err != nil {
		return Params{}, fmt.Errorf("runTime parse error: %w", err)
	}
	params := Params{
		CaseName:       cmd[0],
		ErrorBreak:     true,
		OpenIDPrefix:   cmd[1],
		OpenIDStart:    0,
		OpenIDEnd:      userCount,
		TargetQPS:      targetQPS,
		UserBatchCount: userBatchCount,
		RunTime:        runTime,
	}
	if len(cmd) > 6 {
		params.ExtraArgs = cmd[6:]
	}
	return params, nil
}

// ParseCaseFileContent 解析 case 文件内容，返回混合了控制指令和压测行的统一行列表。
// 支持 # 注释、行尾 & 后台标记、@ 控制指令前缀。
// mode 决定压测行的解析格式：CaseFileModeStandalone 使用单机格式，CaseFileModeDistributed 使用分布式格式。
func ParseCaseFileContent(content string, mode CaseFileMode) ([]CaseFileLine, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))

	var lines []CaseFileLine
	for scanner.Scan() {
		raw := scanner.Text()
		// 去注释
		if idx := strings.Index(raw, "#"); idx >= 0 {
			raw = raw[:idx]
		}
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		args := strings.Fields(line)

		// 检测行尾 & 后台标记
		background := false
		if len(args) > 0 && strings.ToLower(args[len(args)-1]) == "&" {
			background = true
			args = args[:len(args)-1]
		}
		if len(args) == 0 {
			continue
		}

		caseIndex := len(lines)

		if IsControlLine(args[0]) {
			// 控制指令行（两种模式均使用相同格式）
			cp, err := ParseControlLine(args)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", caseIndex, err)
			}
			cp.CaseIndex = caseIndex
			cp.Background = background
			lines = append(lines, CaseFileLine{
				IsControl:  true,
				Background: background,
				Control:    cp,
			})
		} else {
			// 压测行：根据模式选择解析函数
			var params Params
			var err error
			switch mode {
			case CaseFileModeDistributed:
				params, err = ParseDistributedLine(args)
			default: // CaseFileModeStandalone
				params, err = ParseStandaloneLine(args)
			}
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", caseIndex, err)
			}
			params.CaseIndex = caseIndex
			lines = append(lines, CaseFileLine{
				IsControl:  false,
				Background: background,
				Stress:     params,
			})
		}
	}
	return lines, nil
}
