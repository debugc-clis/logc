package main

import (
	"fmt"
	"io"
)

func completionCommand(args []string, stdout, stderr io.Writer) int {
	const help = "logc: usage: logc completion bash"
	if len(args) == 1 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Fprintln(stderr, help)
			return 0
		case "bash":
			if _, err := io.WriteString(stdout, bashCompletionScript); err != nil {
				fmt.Fprintln(stderr, "logc completion:", err)
				return 1
			}
			return 0
		}
	}
	fmt.Fprintln(stderr, help)
	return 2
}

const bashCompletionScript = `# Bash 3.2+ completion for logc; no runtime logc or external commands.
_logc_completion()
{
    local current mode start index word expecting candidates values
    local IFS=$' \t\n'
    COMPREPLY=()
    current=${COMP_WORDS[COMP_CWORD]}
    mode=root
    start=1
    if (( COMP_CWORD > 1 )); then
        case "${COMP_WORDS[1]}" in
            watch|system|docker|ls|where|config|completion)
                mode=${COMP_WORDS[1]}
                start=2
                ;;
            list) mode=ls; start=2 ;;
            help|version|--version|-version|init-config|show-config) return 0 ;;
        esac
    fi
    case "$mode" in
        root)
            candidates='-f --follow --since --all -C --context -i --ignore-case -n --lines --dedup --no-color --json --current --category --module --exclude -m --match -h --help'
            values=' --since -C --context -n --lines --category --module --exclude -m --match '
            if (( COMP_CWORD == 1 )); then
                candidates="watch system docker ls where config completion help version --version $candidates"
            fi
            ;;
        watch)
            candidates='--since -i --ignore-case -n --lines --no-color --category --module --exclude --full -m --match -h --help'
            values=' --since -C --context -n --lines --category --module --exclude -m --match '
            ;;
        system)
            candidates='--kernel --since --all -n --lines -i --ignore-case -C --context --dedup --no-color --json -m --match -h --help'
            values=' --since -C --context -n --lines --category --module --exclude -m --match '
            ;;
        docker)
            candidates='--since --until --tail -n --timestamps -t --details --follow -f -m --match -i --ignore-case -C --context --dedup --no-color --json -h --help'
            values=' --since --until --tail -n -m --match -C --context '
            ;;
        ls)
            candidates='--category --module --json'
            values=' --category --module '
            ;;
        where) candidates='--json'; values='' ;;
        config)
            if (( COMP_CWORD == 2 )); then
                candidates='init path show help -h --help'
            elif (( COMP_CWORD == 3 )) && [[ ${COMP_WORDS[2]} == init ]]; then
                candidates='--force'
            else
                return 0
            fi
            values=''
            ;;
        completion)
            if (( COMP_CWORD == 2 )); then
                candidates='bash help -h --help'
            else
                return 0
            fi
            values=''
            ;;
    esac
    expecting=0
    for (( index=start; index<COMP_CWORD; index++ )); do
        word=${COMP_WORDS[index]}
        if (( expecting )); then
            expecting=0
            if [[ $word == : || ${COMP_WORDS[index+1]} == : ]]; then
                expecting=1
            fi
            if [[ $mode == docker && $word == = ]]; then
                case "${COMP_WORDS[index-1]}" in
                    --since|--until|--tail) expecting=1 ;;
                esac
            fi
        elif [[ $word == -- ]]; then
            return 0
        elif [[ $values == *" $word "* ]]; then
            expecting=1
        elif [[ $mode == docker && $word == -*n && $word != --* ]]; then
            expecting=1
        fi
    done
    if (( expecting )); then
        return 0
    fi
    COMPREPLY=( $(compgen -W "$candidates" -- "$current") )
    return 0
}
complete -o default -o bashdefault -F _logc_completion logc
`
