# First conversation in about five minutes

Install [MARSHAL](installation.md) and at least one supported
[provider CLI](providers.md), and complete that provider's own sign-in.
This walkthrough starts a planning conversation; completing a worker run takes
longer and depends on the provider and project.

1. Enter your existing Git project and initialize it:

    ```bash
    marshal init
    marshal doctor
    marshal doctor --probe-providers
    marshal tui
    ```

2. In the composer, select your installed planning provider:

    ```text
    /marshal model codex
    /marshal chat
    ```

    Replace `codex` with `claude` or `agy` if that is your provider.
    Bare `/marshal` displays status and usage; it does not start a chat.

3. Answer the Marshal's questions about language, goal, constraints and desired
   result. Try a small goal such as adding a documented test for an existing
   function. Ask it to prepare a plan pack.
4. Leave the provider conversation with `/exit`. MARSHAL displays the plan
   location. Read the requirements and each task before approving.
5. Follow the [Marshal workflow](marshal.md) to approve, observe, review and
   accept the work.

Plain composer text does not launch an agent. Choose an explicit command.
Keep the MARSHAL window open while work runs. Initialization preserves existing
regular project defaults and creates missing defaults and local state.
