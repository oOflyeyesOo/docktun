package service

import (
	"bufio"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const runRedacted = "[redacted]"

// RunCommandRedactor is only used for presentation. The original command and
// Docker arguments must always be passed to execution unchanged.
type RunCommandRedactor struct {
	replacer     *strings.Replacer
	shortSecrets []string
}

func environmentArgument(args []string, i int) (prefix, assignment string, next bool) {
	arg := args[i]
	if (arg == "-e" || arg == "--env") && i+1 < len(args) {
		return "", args[i+1], true
	}
	for _, flag := range []string{"--env=", "-e="} {
		if strings.HasPrefix(arg, flag) {
			return flag, strings.TrimPrefix(arg, flag), false
		}
	}
	if strings.HasPrefix(arg, "-e") && !strings.HasPrefix(arg, "--") && len(arg) > 2 {
		return "-e", arg[2:], false
	}
	return "", "", false
}

func redactedAssignment(assignment string) string {
	key, _, assigned := strings.Cut(assignment, "=")
	if !assigned {
		return assignment // An inherited environment variable contains no value.
	}
	return key + "=" + runRedacted
}

func RedactDockerRunArgs(args []string) string {
	copyArgs := append([]string(nil), args...)
	for i := 0; i < len(copyArgs); i++ {
		prefix, assignment, next := environmentArgument(copyArgs, i)
		if next {
			i++
			copyArgs[i] = redactedAssignment(assignment)
		} else if assignment != "" {
			copyArgs[i] = prefix + redactedAssignment(assignment)
		}
	}
	for i, arg := range copyArgs {
		if strings.ContainsAny(arg, " \t\r\n\"'") {
			copyArgs[i] = strconv.Quote(arg)
		}
	}
	return strings.Join(copyArgs, " ")
}

func RedactDockerRunCommand(command string) string {
	return RedactDockerRunArgs(splitShellArgs(command))
}

func NewRunCommandRedactor(command string) *RunCommandRedactor {
	args := splitShellArgs(command)
	replacements := map[string]string{runRedacted: runRedacted}
	shortSecrets := map[string]bool{}
	for i := 0; i < len(args); i++ {
		_, assignment, next := environmentArgument(args, i)
		if next {
			i++
		}
		key, value, assigned := strings.Cut(assignment, "=")
		if !assigned || value == "" {
			continue
		}
		replacements[assignment] = key + "=" + runRedacted
		// Avoid replacing every occurrence of ordinary short configuration
		// values (for example FLAG=1) in logs. Credential assignments and all
		// command previews are masked regardless of their value's length.
		upper := strings.ToUpper(key)
		for _, sensitive := range []string{"PASSWORD", "PASSWD", "TOKEN", "SECRET", "KEY", "CREDENTIAL", "DATABASE_URL", "CONNECTION_STRING", "DSN", "AUTH"} {
			if strings.Contains(upper, sensitive) || upper == "PASS" || upper == "PWD" {
				if len(value) >= 4 {
					replacements[value] = runRedacted
				} else {
					shortSecrets[value] = true
				}
				for _, line := range strings.FieldsFunc(value, func(r rune) bool { return r == '\r' || r == '\n' }) {
					if len(line) >= 4 {
						replacements[line] = runRedacted
					} else {
						shortSecrets[line] = true
					}
				}
				break
			}
		}
	}
	keys := make([]string, 0, len(replacements))
	for key := range replacements {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	pairs := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		pairs = append(pairs, key, replacements[key])
	}
	short := make([]string, 0, len(shortSecrets))
	for value := range shortSecrets {
		short = append(short, value)
	}
	sort.Slice(short, func(i, j int) bool { return len(short[i]) > len(short[j]) })
	return &RunCommandRedactor{replacer: strings.NewReplacer(pairs...), shortSecrets: short}
}

func (r *RunCommandRedactor) Redact(output string) string {
	if r == nil || r.replacer == nil {
		return output
	}
	output = r.replacer.Replace(output)
	for _, secret := range r.shortSecrets {
		output = redactShortRunSecret(output, secret)
	}
	return output
}

// Tiny credentials are masked as complete values without replacing characters
// inside unrelated words, IDs or the redaction marker itself.
func redactShortRunSecret(output, secret string) string {
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	var result strings.Builder
	for cursor := 0; ; {
		found := strings.Index(output[cursor:], secret)
		if found < 0 {
			result.WriteString(output[cursor:])
			return result.String()
		}
		position := cursor + found
		end := position + len(secret)
		before, _ := utf8.DecodeLastRuneInString(output[:position])
		after, _ := utf8.DecodeRuneInString(output[end:])
		result.WriteString(output[cursor:position])
		if (position == 0 || !word(before)) && (end == len(output) || !word(after)) {
			result.WriteString(runRedacted)
		} else {
			result.WriteString(secret)
		}
		cursor = end
	}
}

// Docker can split any credential across pipe reads. Assemble complete bounded
// lines before redaction, and discard oversized lines while continuing to drain
// the pipe so a noisy child cannot deadlock Wait or retain unbounded memory.
func streamRunOutput(source io.Reader, emit func(string)) {
	const maximumLine = 64 << 10
	reader := bufio.NewReader(source)
	line := make([]byte, 0, 4096)
	discarding := false
	for {
		part, err := reader.ReadSlice('\n')
		if !discarding {
			if len(line)+len(part) > maximumLine {
				line = line[:0]
				discarding = true
				emit("[WARN] An oversized Docker output line was omitted.")
			} else {
				line = append(line, part...)
			}
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			if !discarding {
				if message := strings.TrimSpace(string(line)); message != "" {
					emit(message)
				}
			}
			line = line[:0]
			discarding = false
		}
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			return
		}
	}
}
