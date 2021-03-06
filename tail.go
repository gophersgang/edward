package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/hpcloud/tail"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"github.com/yext/edward/config"
	"github.com/yext/edward/runner"
	"github.com/yext/edward/services"
)

func tailFromFlag(c *cli.Context) error {
	fmt.Println("=== Logs ===")
	return errors.WithStack(doLog(c))
}

func doLog(c *cli.Context) error {
	if len(c.Args()) == 0 {
		return errors.New("At least one service or group must be specified")
	}
	sgs, err := config.GetServicesOrGroups(c.Args())
	if err != nil {
		return errors.WithStack(err)
	}

	var logChannel = make(chan runner.LogLine)
	var lines []runner.LogLine
	for _, sg := range sgs {
		switch v := sg.(type) {
		case *services.ServiceConfig:
			newLines, err := followServiceLog(v, logChannel)
			if err != nil {
				return err
			}
			lines = append(lines, newLines...)
		case *services.ServiceGroupConfig:
			newLines, err := followGroupLog(v, logChannel)
			if err != nil {
				return err
			}
			lines = append(lines, newLines...)
		}
	}

	// Sort initial lines
	sort.Sort(byTime(lines))
	for _, line := range lines {
		printMessage(line, services.CountServices(sgs) > 1)
	}

	for logMessage := range logChannel {
		printMessage(logMessage, services.CountServices(sgs) > 1)
	}

	return nil
}

type byTime []runner.LogLine

func (a byTime) Len() int           { return len(a) }
func (a byTime) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byTime) Less(i, j int) bool { return a[i].Time.Before(a[j].Time) }

func printMessage(logMessage runner.LogLine, multiple bool) {

	message := strings.TrimSpace(logMessage.Message)

	if len(message) == 0 {
		return
	}

	if multiple {
		print("[")
		color.Set(color.FgHiYellow)
		print(logMessage.Name)
		if logMessage.Stream == "messages" {
			print(" (edward)")
		}
		color.Unset()
		print("]: ")
	}

	if logMessage.Stream == "stderr" {
		color.Set(color.FgRed)
	}
	if logMessage.Stream == "messages" {
		color.Set(color.FgYellow)
	}

	fmt.Printf("%v\n", strings.TrimSpace(message))
	color.Unset()
}

func followGroupLog(group *services.ServiceGroupConfig, logChannel chan runner.LogLine) ([]runner.LogLine, error) {
	var lines []runner.LogLine
	for _, group := range group.Groups {
		newLines, err := followGroupLog(group, logChannel)
		lines = append(lines, newLines...)
		if err != nil {
			return nil, err
		}
	}
	for _, service := range group.Services {
		newLines, err := followServiceLog(service, logChannel)
		lines = append(lines, newLines...)
		if err != nil {
			return nil, err
		}
	}
	return lines, nil
}

func followServiceLog(service *services.ServiceConfig, logChannel chan runner.LogLine) ([]runner.LogLine, error) {
	// Skip services that don't have a launch command
	if service.Commands.Launch == "" {
		return nil, nil
	}

	runLog := service.GetRunLog()
	logFile, err := os.Open(runLog)
	defer logFile.Close()
	if err != nil {
		return nil, errors.WithStack(err)
	}
	var initialLines []runner.LogLine
	// create a new scanner and read the file line by line
	scanner := bufio.NewScanner(logFile)
	var lineCount int
	for scanner.Scan() {
		text := scanner.Text()
		lineCount++
		var line runner.LogLine
		line, err = runner.ParseLogLine(text)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		initialLines = append(initialLines, line)
	}

	// check for errors
	if err = scanner.Err(); err != nil {
		return nil, errors.WithStack(err)
	}

	go doFollowServiceLog(service, lineCount, logChannel)
	return initialLines, nil
}

func doFollowServiceLog(service *services.ServiceConfig, skipLines int, logChannel chan runner.LogLine) error {
	runLog := service.GetRunLog()
	t, err := tail.TailFile(runLog, tail.Config{
		Follow: true,
		Logger: tail.DiscardingLogger,
	})
	if err != nil {
		return errors.WithStack(err)
	}
	var linesSkipped int
	for line := range t.Lines {
		if linesSkipped < skipLines {
			linesSkipped++
			continue
		}
		lineData, err := runner.ParseLogLine(line.Text)
		if err != nil {
			return errors.WithStack(err)
		}
		logChannel <- lineData
	}
	return nil
}
