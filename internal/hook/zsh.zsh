# Kagegami shell hook for zsh. Load it with: eval "$(kagegami hook zsh)"
# It prints invisible markers around every command while kagegami watches this pane, and does nothing otherwise.
# Built-ins only: no external process runs per command. The nonce is never kept in a variable between commands,
# so `set`, `typeset` and `env` cannot print it.
if [[ -n $TMUX_PANE && -z $__kg_loaded ]]; then
  typeset -g __kg_loaded=1 __kg_dir=@RUN_DIR@ __kg_id=0 __kg_open=0 __kg_on=0 __kg_prompt_sp=0

  # Sets REPLY to the nonce of this pane, or to nothing when kagegami is not watching it.
  __kg_read() {
    emulate -L zsh -o extendedglob
    REPLY=
    local n f=$__kg_dir/${TMUX_PANE#%}.nonce
    [[ -r $f ]] && read -r n < $f 2>/dev/null
    [[ $n == [0-9a-f](#c32) ]] && REPLY=$n
  }

  # PROMPT_SP follows the watch state ($1 is the nonce, empty when not watching): off while watched, as the user had it otherwise.
  __kg_sync() {
    if [[ -n $1 ]]; then
      if (( ! __kg_on )); then
        __kg_on=1; [[ -o promptsp ]] && __kg_prompt_sp=1 || __kg_prompt_sp=0
        unsetopt promptsp
      fi
    elif (( __kg_on )); then
      __kg_on=0
      (( __kg_prompt_sp )) && setopt promptsp
    fi
  }

  __kg_preexec() {
    __kg_read
    local n=$REPLY
    REPLY=
    __kg_sync $n
    if [[ -z $n ]]; then
      __kg_open=0
      return
    fi
    (( __kg_id++ ))
    __kg_open=1
    local c=${1//[[:cntrl:]]/ }
    print -rn -- $'\e]7771;C;kg='$n';id='$__kg_id';cmd='$c$'\a'
  }

  __kg_precmd() {
    local rc=$?
    __kg_read
    local n=$REPLY
    REPLY=
    if (( __kg_open )); then
      __kg_open=0
      [[ -n $n ]] && print -rn -- $'\e]7771;D;kg='$n';id='$__kg_id';rc='$rc$'\a'
    fi
    __kg_sync $n
  }

  # The start marker goes last, right before the command. The end marker goes first, so that what the user's other
  # precmd hooks print is not taken for output of the command. (The old single `precmd` function always runs
  # before any hook list; its output cannot be kept out of the command's output.)
  autoload -Uz add-zsh-hook
  add-zsh-hook preexec __kg_preexec
  precmd_functions=(__kg_precmd ${precmd_functions:#__kg_precmd})
fi
