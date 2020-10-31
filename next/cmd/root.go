package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	doesNotRequireValidConfig    = "chezmoi_annotation_does_not_require_valid_config"
	modifiesConfigFile           = "chezmoi_annotation_modifies_config_file"
	modifiesDestinationDirectory = "chezmoi_annotation_modifies_destination_directory"
	modifiesSourceDirectory      = "chezmoi_annotation_modifies_source_directory"
	requiresConfigDirectory      = "chezmoi_annotation_requires_config_directory"
	requiresSourceDirectory      = "chezmoi_annotation_requires_source_directory"
	runsCommands                 = "chezmoi_annotation_runs_commands"
)

// An ErrExitCode indicates the the main program should exit with the given
// code.
type ErrExitCode int

func (e ErrExitCode) Error() string { return "" }

// A VersionInfo contains a version.
type VersionInfo struct {
	Version string
	Commit  string
	Date    string
	BuiltBy string
}

func (c *Config) newRootCmd() (*cobra.Command, error) {
	rootCmd := &cobra.Command{
		Use:                "chezmoi",
		Short:              "Manage your dotfiles across multiple diverse machines, securely",
		Version:            c.versionStr,
		PersistentPreRunE:  c.persistentPreRunRootE,
		PersistentPostRunE: c.persistentPostRunRootE,
		SilenceErrors:      true,
		SilenceUsage:       true,
	}

	persistentFlags := rootCmd.PersistentFlags()

	persistentFlags.StringVar(&c.Color, "color", c.Color, "colorize diffs")
	persistentFlags.VarP(&c.DestDir, "destination", "D", "destination directory")
	persistentFlags.StringVar(&c.Format, "format", c.Format, "format ("+serializationFormatNamesStr()+")")
	persistentFlags.BoolVar(&c.Remove, "remove", c.Remove, "remove targets")
	persistentFlags.VarP(&c.SourceDir, "source", "S", "source directory")
	for _, key := range []string{
		"color",
		"destination",
		"format",
		"remove",
		"source",
	} {
		if err := viper.BindPFlag(key, persistentFlags.Lookup(key)); err != nil {
			return nil, err
		}
	}

	persistentFlags.StringVarP(&c.configFile, "config", "c", c.configFile, "config file")
	persistentFlags.BoolVarP(&c.dryRun, "dry-run", "n", c.dryRun, "dry run")
	persistentFlags.BoolVar(&c.force, "force", c.force, "force")
	persistentFlags.BoolVarP(&c.verbose, "verbose", "v", c.verbose, "verbose")
	persistentFlags.StringVarP(&c.output, "output", "o", c.output, "output file")
	persistentFlags.BoolVar(&c.debug, "debug", c.debug, "write debug logs")

	for _, err := range []error{
		rootCmd.MarkPersistentFlagFilename("config"),
		rootCmd.MarkPersistentFlagDirname("destination"),
		rootCmd.MarkPersistentFlagFilename("output"),
		rootCmd.MarkPersistentFlagDirname("source"),
	} {
		if err != nil {
			return nil, err
		}
	}

	// FIXME this shouldn't be global
	// FIXME move it to c.persistentPreRunRootE
	cobra.OnInitialize(func() {
		v := viper.New()
		v.SetConfigFile(c.configFile)
		err := v.ReadInConfig()
		if os.IsNotExist(err) {
			return
		}
		c.err = err
		if c.err == nil {
			c.err = v.Unmarshal(c)
		}
		if c.err == nil {
			c.err = c.validateData()
		}
		if c.err != nil {
			rootCmd.Printf("warning: %s: %v\n", c.configFile, c.err)
		}
	})

	rootCmd.SetHelpCommand(c.newHelpCmd(rootCmd))
	for _, newCmdFunc := range []func() *cobra.Command{
		c.newAddCmd,
		c.newApplyCmd,
		c.newArchiveCmd,
		c.newCatCmd,
		c.newCDCmd,
		c.newChattrCmd,
		c.newCompletionCmd,
		c.newDataCmd,
		c.newDiffCmd,
		c.newDocsCmd,
		c.newDoctorCmd,
		c.newDumpCmd,
		c.newEditCmd,
		c.newEditConfigCmd,
		c.newExecuteTemplateCmd,
		c.newForgetCmd,
		c.newGitCmd,
		// c.newImportCmd, // FIXME
		c.newInitCmd,
		c.newManagedCmd,
		c.newMergeCmd,
		c.newPurgeCmd,
		c.newRemoveCmd,
		c.newSourcePathCmd,
		c.newStateCmd,
		c.newStatusCmd,
		c.newUnmanagedCmd,
		c.newUpdateCmd,
		c.newVerifyCmd,
	} {
		rootCmd.AddCommand(newCmdFunc())
	}

	return rootCmd, nil
}

// Execute executes the root command.
func Execute(v VersionInfo) error {
	c, err := newConfig(
		withVersionInfo(v),
	)
	if err != nil {
		return err
	}
	rootCmd, err := c.newRootCmd()
	if err != nil {
		return err
	}
	return rootCmd.Execute()
}

func getAsset(name string) ([]byte, error) {
	asset, ok := assets[name]
	if !ok {
		return nil, fmt.Errorf("%s: not found", name)
	}
	return asset, nil
}

func getBoolAnnotation(cmd *cobra.Command, key string) bool {
	value, ok := cmd.Annotations[key]
	if !ok {
		return false
	}
	boolValue, err := strconv.ParseBool(value)
	if err != nil {
		panic(err)
	}
	return boolValue
}

func getExample(command string) string {
	return helps[command].example
}

func mustGetLongHelp(command string) string {
	help, ok := helps[command]
	if !ok {
		panic(fmt.Sprintf("%s: no long help", command))
	}
	return help.long
}
