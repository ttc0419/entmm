# ENT Mermaid ERD Generator
A tool to generate Mermaid ERD from ent schema which you can visualize
on https://mermaid.live/ or https://mermaid-live.nodejs.cn/

## Quick Start

Open the generated ERD in the official Mermaid Live editor:

```shell
go run github.com/ttc0419/entmm -w <schema-path>
```

Include entity fields in the generated ERD:

```shell
go run github.com/ttc0419/entmm -attr <schema-path>
```

For users in China, use `-cn` to open the China mirror:

```shell
go run github.com/ttc0419/entmm -w -cn <schema-path>
```

## Flags

- `-w`: open the generated ERD in Mermaid Live.
- `-cn`: use `https://mermaid-live.nodejs.cn/` with `-w`.
- `-o <file>`: write the generated Mermaid ERD to a file.
- `-attr`: include entity fields. Fields are omitted by default.
