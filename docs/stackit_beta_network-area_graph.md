## stackit beta network-area graph

Opens an interactive browser graph of a STACKIT Network Area

### Synopsis

Spins up a local web server to display an interactive, force-directed graph of a Network Area, its Projects, Networks, Interfaces, and Routing Tables.

```
stackit beta network-area graph AREA_ID [flags]
```

### Examples

```
  Visualize network area with ID "xxx" in organization with ID "yyy"
  $ stackit network-area graph xxx --organization-id yyy

  Visualize network area with a custom 500ms delay between API calls
  $ stackit network-area graph xxx --organization-id yyy --api-delay 500
```

### Options

```
      --api-delay int            Delay between API calls in milliseconds to avoid rate limits (default 200)
  -h, --help                     Help for "stackit beta network-area graph"
      --organization-id string   Organization ID
```

### Options inherited from parent commands

```
  -y, --assume-yes             If set, skips all confirmation prompts
      --async                  If set, runs the command asynchronously
  -o, --output-format string   Output format, one of ["json" "pretty" "none" "yaml"]
  -p, --project-id string      Project ID
      --region string          Target region for region-specific requests
      --verbosity string       Verbosity of the CLI, one of ["debug" "info" "warning" "error"] (default "info")
```

### SEE ALSO

* [stackit beta network-area](./stackit_beta_network-area.md)	 - Provides beta functionality for Network Area

