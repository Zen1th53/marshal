# ULTRA

ULTRA is an optional extra mode of MARSHAL that runs on the MARSHAL Cloud.
You need access to use it.

## Check whether you have access

```text
/ultra status
```

MARSHAL shows one of three states:

- ULTRA is running in this session,
- you have access, but ULTRA is switched off,
- you do not have access.

If you do not have access, you can ask for it:

```text
/ultra request
```

## Switch it on

ULTRA is always off when you start MARSHAL. To switch it on for this session:

```text
/ultra start
```

While it is on, the top of the window shows `ULTRA ACTIVE`.

## Switch it off

```text
/ultra stop
/ultra stop confirm
```

Type the second line straight after the first. MARSHAL asks for the
confirmation so that ULTRA is not switched off by accident.

!!! note
    You stay in charge with ULTRA too: it never approves anything on your
    behalf.
