package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

var COMMAND Command

func init() {
	flag.StringVar(&COMMAND.command, "command", "", "full commmand to run")
	flag.DurationVar(&COMMAND.timeoutDuration, "timeout-duration", 60*time.Second, "timeout duration")
	flag.StringVar(&COMMAND.stdin, "stdin", "", "stdin input string")

	// stdin rule
	flag.StringVar(&COMMAND.promptMatchRule.startsWith, "prompt-startsWith", "", "when promote output startswith this message, write '--stdin' into stdin")
	flag.StringVar(&COMMAND.promptMatchRule.endsWith, "prompt-endsWith", "", "when promote output endswith this message, write '--stdin' into stdin")
	flag.StringVar(&COMMAND.promptMatchRule.contains, "prompt-contains", "", "when promote output contain this message, write '--stdin' into stdin")
	flag.StringVar(&COMMAND.promptMatchRule.regex, "prompt-matchRegex", "", "when promote output match by this regex, write '--stdin' into stdin")

	//
	flag.BoolVar(&COMMAND.escapePrompt, "escape-prompt", false, "escape prompt")
	flag.BoolVar(&COMMAND.escapeStdout, "escape-stdout", false, "escape stdout")
	flag.BoolVar(&COMMAND.escapeStderr, "escape-stderr", false, "escape stderr")
	flag.BoolVar(&COMMAND.verbose, "verbose", false, "verbose output")
	flag.BoolVar(&COMMAND.quiet, "quiet", false, "ignore all stdout or stderr, nozero indicate command exec failed")
}

func main() {
	flag.Parse()
	if COMMAND.command == "" {
		flag.Usage()
		os.Exit(1)
	}
	if COMMAND.promptMatchRule.regex != "" {
		if _, _err := regexp.Compile(COMMAND.promptMatchRule.regex); _err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] compile regex \"%s\" faile, Error: \"%v\"\n", COMMAND.promptMatchRule.regex, _err)
			os.Exit(1)
		}
	}
	(&COMMAND).run()
	if !COMMAND.quiet {
		if _bs := COMMAND.stdout.Bytes(); len(_bs) != 0 {
			if COMMAND.escapeStdout {
				fmt.Fprintf(os.Stdout, "%q", COMMAND.stdout.Bytes())
			} else {
				fmt.Fprintf(os.Stdout, "%s", COMMAND.stdout.Bytes())
			}
		}
		if _bs := COMMAND.stderr.Bytes(); len(_bs) != 0 {
			if COMMAND.escapeStderr {
				fmt.Fprintf(os.Stderr, "%q", COMMAND.stderr.Bytes())
			} else {
				fmt.Fprintf(os.Stderr, "%s", COMMAND.stderr.Bytes())
			}
		}
	}
	os.Exit(COMMAND.exitCode)
}

type Rule struct {
	startsWith string
	endsWith   string
	contains   string
	regex      string
}

func (rule *Rule) matchRule(str string) bool {
	return ruleStartsWith(str, rule.startsWith) && ruleEndsWith(str, rule.endsWith) && ruleContains(str, rule.contains) && ruleRegex(str, rule.regex)
}

func ruleStartsWith(str string, rule string) bool {
	return len(rule) == 0 || strings.HasPrefix(str, rule)
}

func ruleEndsWith(str string, rule string) bool {
	return len(rule) == 0 || strings.HasSuffix(str, rule)
}

func ruleContains(str string, rule string) bool {
	return len(rule) == 0 || strings.Contains(str, rule)
}

func ruleRegex(str string, rule string) bool {
	if len(rule) == 0 {
		return true
	}
	_matched, _err := regexp.MatchString(rule, str)
	if _err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", _err)
	}
	return _matched
}

type Command struct {
	command         string
	timeoutDuration time.Duration
	promptMatchRule Rule
	stdin           string
	stdout          bytes.Buffer
	stderr          bytes.Buffer
	exitCode        int
	execSuccess     bool
	errString       string
	escapePrompt    bool
	escapeStdout    bool
	escapeStderr    bool
	verbose         bool
	quiet           bool
}

func (cmd *Command) run() {

	// FIEME
	// command like "sudo su -c 'sleep 10'" make timeout not work
	ctx, cancel := context.WithTimeout(context.Background(), cmd.timeoutDuration)
	defer cancel()

	_cmds := splitCmdline(cmd.command)
	_cmdCtx := exec.CommandContext(ctx, _cmds[0], _cmds[1:]...)

	_cmdCtx.Stderr = &(cmd.stderr)
	_cmdCtx.Stdout = &(cmd.stdout)

	fd, _err := pty.Start(_cmdCtx)
	defer fd.Close()
	if _err != nil {
		panic(_err)
	}

	_rf := bufio.NewReader(fd)

	if cmd.stdin != "" {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			var _outBuff bytes.Buffer
			for {
				_b, _err := _rf.ReadByte()
				if err := _outBuff.WriteByte(_b); err != nil {
					if cmd.quiet {
						fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
					}
				}
				if _rf.Buffered() == 0 {
					if cmd.verbose {
						if cmd.escapePrompt {
							fmt.Fprintf(os.Stderr, "[DEBUG] [Prompt] '%q'\n", _outBuff.Bytes())
						} else {
							fmt.Fprintf(os.Stderr, "[DEBUG] [Prompt] '%s'\n", _outBuff.Bytes())
						}
					}
					if cmd.promptMatchRule.matchRule(_outBuff.String()) {
						fd.WriteString(cmd.stdin + "\n")
					}
					break
				}
				if _err != nil {
					break
				}
			}
			wg.Done()
		}()
		wg.Wait()
	}
	if _cmdErr := _cmdCtx.Wait(); _cmdErr == nil {
		cmd.execSuccess = true
	} else {
		cmd.errString = _cmdErr.Error()
	}
	if _ctxErr := ctx.Err(); _ctxErr == nil {
		cmd.execSuccess = cmd.execSuccess && true
	} else {
		cmd.execSuccess = cmd.execSuccess && false
		cmd.errString = _ctxErr.Error()
	}
	cmd.exitCode = _cmdCtx.ProcessState.ExitCode()
}

func splitCmdline(cmdline string) []string {
	if len(cmdline) == 0 {
		return []string{}
	}
	cmds := make([]string, 0)
	_quotas := [2]bool{false, false}
	var _buf bytes.Buffer
	for _, _rune := range cmdline {
		switch _rune {
		case '\'':
			_quotas[0] = !_quotas[0]
		case '"':
			_quotas[1] = !_quotas[1]
		case ' ':
			if _quotas[0] || _quotas[1] {
				_buf.WriteRune(_rune)
			} else {
				cmds = append(cmds, _buf.String())
				_buf.Reset()
			}
		default:
			_buf.WriteRune(_rune)
		}
	}
	if _buf.Len() > 0 {
		cmds = append(cmds, _buf.String())
	}
	return cmds
}
