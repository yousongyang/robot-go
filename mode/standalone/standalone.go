package standalone

import (
	"flag"
	"fmt"
	"os"

	log "github.com/atframework/atframe-utils-go/log"
	robot_case "github.com/atframework/robot-go/case"
	user_data "github.com/atframework/robot-go/data"
	utils "github.com/atframework/robot-go/utils"
)

func StartStandalone(flagSet *flag.FlagSet) {
	// --- Standalone 模式 ---
	caseFile := flagSet.Lookup("case_file").Value.String()
	if caseFile != "" {
		repeatedTime := utils.GetFlagInt32(flagSet, "case_file_repeated")
		if repeatedTime < 1 {
			repeatedTime = 1
		}
		err := robot_case.RunCaseFileStandAlone(caseFile, repeatedTime, utils.GetSetVars(flagSet))
		if err != nil {
			fmt.Println("Run case file error:", err)
			log.CloseAllLogWriters()
			os.Exit(1)
		}
	} else {
		utils.ReadLine()
	}

	utils.StdoutLog("Closing all pending connections")
	user_data.LogoutAllUsers()
	log.CloseAllLogWriters()
	utils.StdoutLog("Exiting....")
}
