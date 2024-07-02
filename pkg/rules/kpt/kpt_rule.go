package kpt

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jenkins-x-plugins/jx-promote/pkg/rules"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cmdrunner"
	"github.com/jenkins-x/jx-helpers/v3/pkg/files"
	"github.com/jenkins-x/jx-logging/v3/pkg/log"
)

// KptRule performs kpt upgrades
func Rule(r *rules.PromoteRule) error {
	config := r.Config
	if config.Spec.KptRule == nil {
		return fmt.Errorf("no appsRule configured")
	}
	rule := config.Spec.KptRule

	gitURL := r.GitURL
	if gitURL == "" {
		return fmt.Errorf("no GitURL for the app so cannot promote via kpt")
	}
	app := r.AppName
	if app == "" {
		return fmt.Errorf("no AppName so cannot promote via kpt")
	}
	version := r.Version

	dir := r.Dir
	namespaceDir := dir
	kptPath := rule.Path
	if kptPath != "" {
		namespaceDir = filepath.Join(dir, kptPath)
	}

	appDir := filepath.Join(namespaceDir, app)
	// if the dir exists lets upgrade otherwise lets add it
	exists, err := files.DirExists(appDir)
	if err != nil {
		return fmt.Errorf("failed to check if the app dir exists %s: %w", appDir, err)
	}

	if version != "" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if version == "" {
		version = "master"
	}
	if r.CommandRunner == nil {
		r.CommandRunner = cmdrunner.DefaultCommandRunner
	}
	if exists {
		// lets upgrade the version via kpt
		args := []string{"pkg", "update", fmt.Sprintf("%s@%s", app, version), "--strategy=alpha-git-patch"}
		c := &cmdrunner.Command{
			Name: "kpt",
			Args: args,
			Dir:  namespaceDir,
		}
		log.Logger().Infof("running command: %s", c.String())
		_, err = r.CommandRunner(c)
		if err != nil {
			return fmt.Errorf("failed to update kpt app %s: %w", app, err)
		}
	} else {
		if gitURL == "" {
			return fmt.Errorf("no gitURL")
		}
		gitURL = strings.TrimSuffix(gitURL, "/")
		if !strings.HasSuffix(gitURL, ".git") {
			gitURL += ".git"
		}
		// lets add the path to the released kubernetes resources
		gitURL += fmt.Sprintf("/charts/%s/resources", app)
		args := []string{"pkg", "get", fmt.Sprintf("%s@%s", gitURL, version), app}
		c := &cmdrunner.Command{
			Name: "kpt",
			Args: args,
			Dir:  namespaceDir,
		}
		log.Logger().Infof("running command: %s", c.String())
		_, err = r.CommandRunner(c)
		if err != nil {
			return fmt.Errorf("failed to get the app %s via kpt: %w", app, err)
		}
	}
	return nil
}
