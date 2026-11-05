# Release notes for 2.4

The importer now retries failed uploads three times, with a ten second wait
between tries. Imports that used to stop on the first error finish.

Run `cadre import --retry 3` to set the count. A value of 0 turns retries off.

The old spelling of the flag, "it's not a retry, it's a re-run", appeared in
an earlier draft and is quoted here only as an exhibit.

```text
Use the flag — not the config file.
```

See [the guide](https://example.test/a--b-c) for the full list of options.
