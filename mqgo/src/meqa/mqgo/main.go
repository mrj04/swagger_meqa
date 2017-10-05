package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/satori/go.uuid"

	"meqa/mqplan"
	"meqa/mqswag"
	"meqa/mqutil"
	"path/filepath"

	"gopkg.in/resty.v0"
	"gopkg.in/yaml.v2"
)

const (
	meqaDataDir = "meqa_data"
	configFile  = ".config"
	resultFile  = "result.yaml"
	serverURL   = "http://localhost:8888"
)

const (
	ConfigAPIKey = "api_key"
)

func getConfigs(meqaPath string) (map[string]interface{}, error) {
	configMap := make(map[string]interface{})
	configPath := filepath.Join(meqaPath, configFile)

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configMap[ConfigAPIKey] = uuid.NewV4().String()
		configBytes, err := yaml.Marshal(configMap)
		if err != nil {
			return nil, err
		}
		err = ioutil.WriteFile(configPath, configBytes, 0644)
		if err != nil {
			return nil, err
		}
		return configMap, nil
	}
	configBytes, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(configBytes, &configMap)
	if err != nil {
		return nil, err
	}
	return configMap, nil
}

func generateMeqa(meqaPath string, swaggerPath string) error {
	resty.SetRedirectPolicy(resty.FlexibleRedirectPolicy(15))

	// Get the API key, if it doesn't exist, generate one.
	configMap, err := getConfigs(meqaPath)
	if err != nil {
		return err
	}
	if configMap[ConfigAPIKey] == nil {
		return errors.New(fmt.Sprintf("api_key not found in %s\n", filepath.Join(meqaPath, configFile)))
	}

	inputBytes, err := ioutil.ReadFile(swaggerPath)
	if err != nil {
		return err
	}

	bodyMap := make(map[string]interface{})
	bodyMap["api_key"] = configMap[ConfigAPIKey]
	bodyMap["swagger"] = string(inputBytes)

	req := resty.R()
	req.SetBody(bodyMap)
	resp, err := req.Post(serverURL + "/specs")

	if status := resp.StatusCode(); status >= 300 {
		return errors.New(fmt.Sprintf("server call failed, status %d, body:\n%s", status, string(resp.Body())))
	}

	respMap := make(map[string]interface{})
	err = json.Unmarshal(resp.Body(), &respMap)
	if err != nil {
		return err
	}

	if respMap["swagger_meqa"] == nil {
		return errors.New(fmt.Sprintf("server call failed, status %d, body:\n%s", resp.StatusCode(), string(resp.Body())))
	}
	err = ioutil.WriteFile(filepath.Join(meqaPath, "swagger_meqa.yaml"), []byte(respMap["swagger_meqa"].(string)), 0644)
	if err != nil {
		return err
	}
	for planName, planBody := range respMap["test_plans"].(map[string]interface{}) {
		err = ioutil.WriteFile(filepath.Join(meqaPath, planName+".yaml"), []byte(planBody.(string)), 0644)
		if err != nil {
			return err
		}
	}

	return nil
}

func main() {
	genCommand := flag.NewFlagSet("generate", flag.ExitOnError)
	genCommand.SetOutput(os.Stdout)
	runCommand := flag.NewFlagSet("run", flag.ExitOnError)
	runCommand.SetOutput(os.Stdout)

	genMeqaPath := genCommand.String("d", meqaDataDir, "the directory where we put meqa temp files and logs")
	genSwaggerFile := genCommand.String("s", filepath.Join(meqaDataDir, "swagger.yaml"), "the swagger.yaml file name or URL")

	runMeqaPath := runCommand.String("d", meqaDataDir, "the directory where we put meqa temp files and logs")
	runSwaggerFile := runCommand.String("s", filepath.Join(meqaDataDir, "swagger_meqa.yaml"), "the swagger.yaml file name or URL")
	testPlanFile := runCommand.String("p", "", "the test plan file name")
	resultFile := runCommand.String("r", filepath.Join(meqaDataDir, resultFile), "the test result file name")
	testToRun := runCommand.String("t", "all", "the test to run")
	username := runCommand.String("u", "", "the username for basic HTTP authentication")
	password := runCommand.String("w", "", "the password for basic HTTP authentication")
	apitoken := runCommand.String("a", "", "the api token for bearer HTTP authentication")
	verbose := runCommand.Bool("v", false, "turn on verbose mode")

	flag.Usage = func() {
		fmt.Println("Usage: mqgo {generate|run} [options]")
		fmt.Println("generate: generate test plans to be used by run command")
		genCommand.PrintDefaults()

		fmt.Println("\nrun: run the tests the in a test plan file")
		runCommand.PrintDefaults()
	}

	if len(os.Args) < 2 {
		flag.Usage()
		os.Exit(1)
	}

	var meqaPath *string
	var swaggerFile *string
	switch os.Args[1] {
	case "generate":
		genCommand.Parse(os.Args[2:])
		meqaPath = genMeqaPath
		swaggerFile = genSwaggerFile
	case "run":
		runCommand.Parse(os.Args[2:])
		meqaPath = runMeqaPath
		swaggerFile = runSwaggerFile
	default:
		flag.Usage()
		os.Exit(1)
	}
	mqutil.Logger = mqutil.NewFileLogger(filepath.Join(*meqaPath, "mqgo.log"))
	mqutil.Logger.Println(os.Args)

	if _, err := os.Stat(*swaggerFile); os.IsNotExist(err) {
		fmt.Printf("can't load swagger file at the following location %s", *swaggerFile)
		return
	}
	fi, err := os.Stat(*meqaPath)
	if os.IsNotExist(err) {
		fmt.Printf("specified meqa directory %s doesn't exist.", *meqaPath)
		return
	}
	if !fi.Mode().IsDir() {
		fmt.Printf("specified meqa directory %s is not a directory.", *meqaPath)
		return
	}

	if genCommand.Parsed() {
		err = generateMeqa(*meqaPath, *swaggerFile)
		if err != nil {
			fmt.Printf("got an err:\n%s", err.Error())
			os.Exit(1)
		}
		return
	}

	mqutil.Verbose = *verbose

	if len(*testPlanFile) == 0 {
		fmt.Println("You must use -p to specify a test plan file. Use -h to see more options.")
		return
	}

	if _, err := os.Stat(*testPlanFile); os.IsNotExist(err) {
		fmt.Printf("can't load test plan file at the following location %s", *testPlanFile)
		return
	}

	// Test loading swagger.json
	swagger, err := mqswag.CreateSwaggerFromURL(*swaggerFile, *meqaPath)
	if err != nil {
		mqutil.Logger.Printf("Error: %s", err.Error())
	}
	mqswag.ObjDB.Init(swagger)

	// Test loading test plan
	mqplan.Current.Username = *username
	mqplan.Current.Password = *password
	mqplan.Current.ApiToken = *apitoken
	err = mqplan.Current.InitFromFile(*testPlanFile, &mqswag.ObjDB)
	if err != nil {
		mqutil.Logger.Printf("Error loading test plan: %s", err.Error())
	}

	if *testToRun == "all" {
		for _, testSuite := range mqplan.Current.SuiteList {
			mqutil.Logger.Printf("\n---\nTest suite: %s\n", testSuite.Name)
			fmt.Printf("\n---\nTest suite: %s\n", testSuite.Name)
			err := mqplan.Current.Run(testSuite.Name, nil)
			mqutil.Logger.Printf("err:\n%v", err)
		}
	} else {
		mqutil.Logger.Printf("\n---\nTest suite: %s\n", *testToRun)
		fmt.Printf("\n---\nTest suite: %s\n", *testToRun)
		err := mqplan.Current.Run(*testToRun, nil)
		mqutil.Logger.Printf("err:\n%v", err)
	}

	os.Remove(*resultFile)
	mqplan.Current.WriteResultToFile(*resultFile)
}
