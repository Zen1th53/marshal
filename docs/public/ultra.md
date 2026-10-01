# ULTRA from the terminal

Every session starts with ULTRA execution off. Inspect availability first:

```text
/ultra status
/ultra start
```

Start enables execution for this session only when ULTRA is available to you.
If it is unavailable, follow the message shown; `/ultra request` requests access.
`/ultra` is also a status command.

Status distinguishes active execution, available access with execution off,
and unavailable access. It may show an access expiry separately from the current
session expiry; older services may not report an access end time. The header and
statusline show `ULTRA ACTIVE` only when access and execution are both enabled.

To stop:

```text
/ultra stop
/ultra stop confirm
```

Type the confirmation as the very next command. `/mode ultra` changes a
supervision preference; it does not enable execution or confer access.
No environment variable enables ULTRA execution. Hard approvals remain yours.
The navigation surface is closed in this build even when ULTRA is available;
use composer commands.
