const _p = require('path').join(require('os').homedir(), '.local/share/TeleAgent/runtimes/node/lib/node_modules');
module.paths.unshift(_p);
const { Document, Packer, Paragraph, TextRun, Table, TableRow, TableCell,
        ImageRun, Header, Footer, AlignmentType, LevelFormat, ExternalHyperlink,
        HeadingLevel, BorderStyle, WidthType, ShadingType, PageNumber, PageBreak,
        VerticalAlign, TableOfContents } = require('docx');
const fs = require('fs');

const tableBorder = { style: BorderStyle.SINGLE, size: 1, color: "CCCCCC" };
const cellBorders = { top: tableBorder, bottom: tableBorder, left: tableBorder, right: tableBorder };

const doc = new Document({
  styles: {
    default: { document: { run: { font: "Microsoft YaHei", size: 24 }, paragraph: { spacing: { line: 360 } } } }
  },
  numbering: {
    config: [
      { reference: "bullet-list-0",
        levels: [{ level: 0, format: LevelFormat.BULLET, text: "\u2022", alignment: AlignmentType.LEFT,
          style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] },
      { reference: "bullet-list-1",
        levels: [{ level: 0, format: LevelFormat.BULLET, text: "\u2022", alignment: AlignmentType.LEFT,
          style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] },
      { reference: "bullet-list-2",
        levels: [{ level: 0, format: LevelFormat.BULLET, text: "\u2022", alignment: AlignmentType.LEFT,
          style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] },
      { reference: "bullet-list-3",
        levels: [{ level: 0, format: LevelFormat.BULLET, text: "\u2022", alignment: AlignmentType.LEFT,
          style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] },
      { reference: "bullet-list-4",
        levels: [{ level: 0, format: LevelFormat.BULLET, text: "\u2022", alignment: AlignmentType.LEFT,
          style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] },
      { reference: "bullet-list-5",
        levels: [{ level: 0, format: LevelFormat.BULLET, text: "\u2022", alignment: AlignmentType.LEFT,
          style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] },
      { reference: "num-list-0",
        levels: [{ level: 0, format: LevelFormat.DECIMAL, text: "%1.", alignment: AlignmentType.LEFT,
          style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] },
      { reference: "num-list-1",
        levels: [{ level: 0, format: LevelFormat.DECIMAL, text: "%1.", alignment: AlignmentType.LEFT,
          style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] }
    ]
  },
  sections: [{
    properties: {
      page: { margin: { top: 1440, right: 1440, bottom: 1440, left: 1440 } }
    },
    headers: {
      default: new Header({ children: [new Paragraph({ alignment: AlignmentType.RIGHT, children: [new TextRun({ text: `Swallow 项目配置文档`, italics: true, size: 18, color: "999999"})] })] })
    },
    footers: {
      default: new Footer({ children: [new Paragraph({
        alignment: AlignmentType.CENTER,
        children: [new TextRun({ text: `第 ` }), new TextRun({ children: [PageNumber.CURRENT] }), new TextRun({ text: ` 页` })]
      })] })
    },
    children: [
      new Paragraph({
        heading: HeadingLevel.HEADING_1,
        outlineLevel: 0,
        spacing: { before: 240, after: 240 },
        children: [new TextRun({ text: `Swallow 项目配置文档`, bold: true, size: 32, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_2,
        outlineLevel: 1,
        spacing: { before: 180, after: 180 },
        children: [new TextRun({ text: `一、项目概述`, bold: true, size: 28, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [new TextRun(`Swallow 是一个基于 Go 语言的 Web 服务项目，使用字节跳动开源的高性能 HTTP 框架 Hertz 构建。项目通过官方脚手架工具 hz 生成标准骨架，日志系统已替换为 uber-go/zap，并配置了 Makefile 用于快速构建与启动。`)]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `技术栈概览`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Table({
        width: { size: 9024, type: WidthType.DXA },
        columnWidths: [2256, 2256, 2256, 2256],
        alignment: AlignmentType.CENTER,
        rows: [
        new TableRow({
          children: [
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `组件`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `名称`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `版本`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `说明`, bold: true })] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`语言`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`Go`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`1.25.12`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`编程语言`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`Web 框架`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`Hertz`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`v0.10.6`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`字节跳动 CloudWeGo 开源 HTTP 框架`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`脚手架`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`hz`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`v0.9.7`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`Hertz 官方代码生成工具`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`日志库`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`zap`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`v1.23.0`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`uber-go 高性能结构化日志`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`日志适配`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`hertz-contrib/logger/zap`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`v1.1.0`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`zap 与 Hertz hlog 的桥接层`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`构建工具`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`GNU Make`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`4.4.1`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`命令行构建自动化`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`C/C++ 编译器`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`GCC / G++`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`15.2.0`)] })]
          }),
          new TableCell({
            width: { size: 2256, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`MinGW-w64（通用编译环境）`)] })]
          })
          ]
        })
        ]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_2,
        outlineLevel: 1,
        spacing: { before: 180, after: 180 },
        children: [new TextRun({ text: `二、环境要求`, bold: true, size: 28, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `2.1 Go 环境`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-0", level: 0 },
        children: [new TextRun({ text: `Go 版本：1.25+` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-0", level: 0 },
        children: [        new TextRun({ text: `GOPATH：` }),
            new TextRun({ text: `C:\\Users\\redmi\\go`, font: "Consolas" })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-0", level: 0 },
        children: [        new TextRun({ text: `GOROOT：` }),
            new TextRun({ text: `C:\\Program Files\\Go`, font: "Consolas" })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-0", level: 0 },
        children: [        new TextRun({ text: `代理配置：` }),
            new TextRun({ text: `GOPROXY=https://goproxy.cn,direct`, font: "Consolas" }),
            new TextRun({ text: `（国内加速）` })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `2.2 构建工具`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [        new TextRun({ text: `以下工具通过 scoop 包管理器安装，位于 ` }),
            new TextRun({ text: `C:\\Users\\redmi\\scoop\\shims\\`, font: "Consolas" }),
            new TextRun({ text: `：` })]
      }),
      new Table({
        width: { size: 9024, type: WidthType.DXA },
        columnWidths: [3008, 3008, 3008],
        alignment: AlignmentType.CENTER,
        rows: [
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `工具`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `安装来源`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `用途`, bold: true })] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`make`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`scoop install make`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`构建命令`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`gcc / g++`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`scoop install gcc`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`C/C++ 编译`)] })]
          })
          ]
        })
        ]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [        new TextRun({ text: `注意`, bold: true }),
            new TextRun({ text: `：安装 scoop 后，新打开的终端窗口才能识别 make、gcc、g++ 命令。若当前终端无法识别，可执行以下命令临时刷新 PATH：` })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `$env:PATH = [Environment]::GetEnvironmentVariable("PATH", "User") + ";" + [Environment]::GetEnvironmentVariable("PATH", "Machine")`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_2,
        outlineLevel: 1,
        spacing: { before: 180, after: 180 },
        children: [new TextRun({ text: `三、项目结构`, bold: true, size: 28, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `D:\\swallow\\`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `├── main.go              # 程序入口，初始化日志并启动 Hertz 服务`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `├── router.go            # 自定义路由注册（手动维护）`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `├── router_gen.go        # 自动生成的路由总入口（勿手动修改）`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `├── go.mod               # Go 模块定义与依赖声明`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `├── go.sum               # 依赖校验文件`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `├── Makefile             # 构建命令配置`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `├── .hz                  # hz 脚手架配置文件`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `├── .gitignore           # Git 忽略规则`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `└── biz\\`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `    ├── handler\\`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `    │   └── ping.go      # Ping handler，返回 {"message":"pong"}`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `    └── router\\`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `        └── register.go  # IDL 自动生成的路由注册入口（勿手动修改）`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `文件职责说明`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [        new TextRun({ text: `main.go`, bold: true }),
            new TextRun({ text: ` — 程序入口` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-1", level: 0 },
        children: [        new TextRun({ text: `调用 ` }),
            new TextRun({ text: `hlog.SetLogger()`, font: "Consolas" }),
            new TextRun({ text: ` 将全局日志器替换为 zap` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-1", level: 0 },
        children: [        new TextRun({ text: `创建 Hertz 实例（` }),
            new TextRun({ text: `server.Default()`, font: "Consolas" }),
            new TextRun({ text: `）` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-1", level: 0 },
        children: [        new TextRun({ text: `调用 ` }),
            new TextRun({ text: `register(h)`, font: "Consolas" }),
            new TextRun({ text: ` 注册全部路由` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-1", level: 0 },
        children: [        new TextRun({ text: `调用 ` }),
            new TextRun({ text: `h.Spin()`, font: "Consolas" }),
            new TextRun({ text: ` 启动服务，默认监听 ` }),
            new TextRun({ text: `:8888`, font: "Consolas" })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [        new TextRun({ text: `router.go`, bold: true }),
            new TextRun({ text: ` — 自定义路由` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-2", level: 0 },
        children: [        new TextRun({ text: `定义 ` }),
            new TextRun({ text: `customizedRegister()`, font: "Consolas" }),
            new TextRun({ text: ` 函数，手动注册业务路由` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-2", level: 0 },
        children: [        new TextRun({ text: `当前已注册 ` }),
            new TextRun({ text: `GET /ping`, font: "Consolas" })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-2", level: 0 },
        children: [new TextRun({ text: `新增路由在此文件中添加` })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [        new TextRun({ text: `router_gen.go`, bold: true }),
            new TextRun({ text: ` — 路由总入口（自动生成，勿修改）` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-3", level: 0 },
        children: [        new TextRun({ text: `定义 ` }),
            new TextRun({ text: `register()`, font: "Consolas" }),
            new TextRun({ text: ` 函数，依次调用 ` }),
            new TextRun({ text: `GeneratedRegister()`, font: "Consolas" }),
            new TextRun({ text: ` 和 ` }),
            new TextRun({ text: `customizedRegister()`, font: "Consolas" })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-3", level: 0 },
        children: [new TextRun({ text: `将 IDL 生成的路由与手动路由统一挂载到 Hertz 实例` })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [        new TextRun({ text: `biz/handler/ping.go`, bold: true }),
            new TextRun({ text: ` — 示例 Handler` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-4", level: 0 },
        children: [        new TextRun({ text: `实现 ` }),
            new TextRun({ text: `Ping()`, font: "Consolas" }),
            new TextRun({ text: ` 函数，返回 ` }),
            new TextRun({ text: `{"message":"pong"}`, font: "Consolas" })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-4", level: 0 },
        children: [new TextRun({ text: `作为新 Handler 的编写参考模板` })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [        new TextRun({ text: `biz/router/register.go`, bold: true }),
            new TextRun({ text: ` — IDL 路由注册（自动生成，勿修改）` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-5", level: 0 },
        children: [        new TextRun({ text: `定义 ` }),
            new TextRun({ text: `GeneratedRegister()`, font: "Consolas" }),
            new TextRun({ text: ` 函数` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-5", level: 0 },
        children: [        new TextRun({ text: `使用 IDL（Thrift/Protobuf）生成代码时，` }),
            new TextRun({ text: `hz`, font: "Consolas" }),
            new TextRun({ text: ` 会将路由注册到此` })]
      }),
      new Paragraph({
        numbering: { reference: "bullet-list-5", level: 0 },
        children: [        new TextRun({ text: `当前无 IDL，内含 ` }),
            new TextRun({ text: `//INSERT_POINT`, font: "Consolas" }),
            new TextRun({ text: ` 占位标记` })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_2,
        outlineLevel: 1,
        spacing: { before: 180, after: 180 },
        children: [new TextRun({ text: `四、依赖配置`, bold: true, size: 28, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `4.1 核心依赖`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [new TextRun(`go.mod 中声明的直接依赖：`)]
      }),
      new Table({
        width: { size: 9024, type: WidthType.DXA },
        columnWidths: [3008, 3008, 3008],
        alignment: AlignmentType.CENTER,
        rows: [
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `依赖`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `版本`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `用途`, bold: true })] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`github.com/cloudwego/hertz`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`v0.10.6`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`Web 框架`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`github.com/hertz-contrib/logger/zap`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`v1.1.0`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`zap 日志适配（当前标记为 indirect，实际被 main.go 引用）`)] })]
          })
          ]
        })
        ]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `4.2 主要间接依赖`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Table({
        width: { size: 9024, type: WidthType.DXA },
        columnWidths: [3008, 3008, 3008],
        alignment: AlignmentType.CENTER,
        rows: [
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `依赖`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `版本`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `用途`, bold: true })] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`github.com/bytedance/sonic`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`v1.15.0`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`高性能 JSON 序列化`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`github.com/cloudwego/netpoll`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`v0.7.5`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`网络库（Hertz 底层传输）`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`go.uber.org/zap`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`v1.23.0`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`结构化日志核心库`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`google.golang.org/protobuf`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`v1.34.1`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`Protobuf 支持`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`github.com/fsnotify/fsnotify`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`v1.5.4`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`文件变更通知`)] })]
          })
          ]
        })
        ]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `4.3 依赖管理命令`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `go mod tidy     # 整理依赖，添加缺失、移除多余`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `go mod download # 下载依赖到本地缓存`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `go mod graph    # 查看依赖关系图`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_2,
        outlineLevel: 1,
        spacing: { before: 180, after: 180 },
        children: [new TextRun({ text: `五、日志配置`, bold: true, size: 28, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `5.1 当前配置`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [        new TextRun({ text: `项目已将 Hertz 默认日志替换为 zap，配置位于 ` }),
            new TextRun({ text: `main.go`, font: "Consolas" }),
            new TextRun({ text: `：` })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `hlog.SetLogger(zaplog.NewLogger())`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [        new TextRun({ text: `通过 ` }),
            new TextRun({ text: `hlog.SetLogger()`, font: "Consolas" }),
            new TextRun({ text: ` 全局替换后，Hertz 内部所有日志输出（启动信息、路由注册、请求日志、错误日志等）均通过 zap 输出。` })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `5.2 自定义 zap 选项`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [        new TextRun({ text: `如需自定义 zap 配置（如输出格式、日志级别、输出到文件等），可通过 ` }),
            new TextRun({ text: `NewLogger()`, font: "Consolas" }),
            new TextRun({ text: ` 的 Option 参数实现：` })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `import (`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `    "go.uber.org/zap"`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `    zaplog "github.com/hertz-contrib/logger/zap"`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `)`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: ``, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `// 示例：带调用者信息的 zap 日志`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `hlog.SetLogger(zaplog.NewLogger(`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `    zaplog.WithZapOptions(zap.AddCaller()),`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `))`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: ``, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `// 示例：同步写文件 + 控制台双输出`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `// 需结合 WithCores 选项配置`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `5.3 可用配置选项`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Table({
        width: { size: 9026, type: WidthType.DXA },
        columnWidths: [4513, 4513],
        alignment: AlignmentType.CENTER,
        rows: [
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `选项函数`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `作用`, bold: true })] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`WithZapOptions(opts ...zap.Option)`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`透传原生 zap.Option`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`WithCoreLevel(lvl zap.AtomicLevel)`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`设置日志级别`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`WithCoreEnc(enc zapcore.Encoder)`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`设置编码器（JSON/Console）`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`WithCoreWs(ws zapcore.WriteSyncer)`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`设置输出目标`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`WithCores(coreConfigs ...CoreConfig)`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`多核心输出（如同时写文件和控制台）`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`WithExtraKeys(keys []ExtraKey) `)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`附加上下文字段`)] })]
          })
          ]
        })
        ]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_2,
        outlineLevel: 1,
        spacing: { before: 180, after: 180 },
        children: [new TextRun({ text: `六、Makefile 命令`, bold: true, size: 28, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Table({
        width: { size: 9026, type: WidthType.DXA },
        columnWidths: [4513, 4513],
        alignment: AlignmentType.CENTER,
        rows: [
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `命令`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `作用`, bold: true })] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`make run`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`直接启动服务（go run .）`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`make build`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`编译为 swallow.exe 二进制文件`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`make tidy`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`整理 Go 依赖（go mod tidy）`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`make clean`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`清理编译产物`)] })]
          })
          ]
        })
        ]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `使用方式`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `cd D:\\swallow`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `make run     # 启动服务`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `make build   # 编译`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `make clean   # 清理`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_2,
        outlineLevel: 1,
        spacing: { before: 180, after: 180 },
        children: [new TextRun({ text: `七、服务运行`, bold: true, size: 28, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `7.1 启动方式`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [new TextRun(`方式一：Make`)]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `make run`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [new TextRun(`方式二：直接运行`)]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `go run .`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `7.2 默认配置`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Table({
        width: { size: 9026, type: WidthType.DXA },
        columnWidths: [4513, 4513],
        alignment: AlignmentType.CENTER,
        rows: [
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `配置项`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `默认值`, bold: true })] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`监听地址`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`0.0.0.0:8888`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`网络库`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`netpoll`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`JSON 序列化`)] })]
          }),
          new TableCell({
            width: { size: 4513, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`bytedance/sonic`)] })]
          })
          ]
        })
        ]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `7.3 接口验证`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [new TextRun(`启动服务后访问 ping 接口：`)]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `curl http://localhost:8888/ping`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [new TextRun(`返回结果：`)]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `{"message":"pong"}`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_2,
        outlineLevel: 1,
        spacing: { before: 180, after: 180 },
        children: [new TextRun({ text: `八、开发指南`, bold: true, size: 28, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `8.1 新增路由`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        numbering: { reference: "num-list-0", level: 0 },
        children: [        new TextRun({ text: `在 ` }),
            new TextRun({ text: `biz/handler/`, font: "Consolas" }),
            new TextRun({ text: ` 下新建 handler 文件（参考 ` }),
            new TextRun({ text: `ping.go`, font: "Consolas" }),
            new TextRun({ text: `）` })]
      }),
      new Paragraph({
        numbering: { reference: "num-list-0", level: 0 },
        children: [        new TextRun({ text: `在 ` }),
            new TextRun({ text: `router.go`, font: "Consolas" }),
            new TextRun({ text: ` 的 ` }),
            new TextRun({ text: `customizedRegister()`, font: "Consolas" }),
            new TextRun({ text: ` 中注册路由` })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [new TextRun(`示例：`)]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `// biz/handler/hello.go`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `package handler`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: ``, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `import (`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `    "context"`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `    "github.com/cloudwego/hertz/pkg/app"`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `    "github.com/cloudwego/hertz/pkg/protocol/consts"`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `)`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: ``, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `func Hello(ctx context.Context, c *app.RequestContext) {`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `    c.JSON(consts.StatusOK, map[string]string{`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `        "message": "hello",`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `    })`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `}`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `// router.go 中添加`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `r.GET("/hello", handler.Hello)`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `8.2 新增依赖`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `go get github.com/xxx/xxx@latest`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `go mod tidy`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_3,
        outlineLevel: 2,
        spacing: { before: 120, after: 120 },
        children: [new TextRun({ text: `8.3 使用 IDL 生成代码`, bold: true, size: 26, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [new TextRun(`若需通过 IDL（Thrift/Protobuf）自动生成路由和 handler 骨架：`)]
      }),
      new Paragraph({
        spacing: { before: 0, after: 0 },
        children: [new TextRun({ text: `hz update -idl api.thrift`, font: "Consolas", size: 20 })]
      }),
      new Paragraph({
        indent: { firstLine: 480 },
        children: [        new TextRun({ text: `生成的路由会自动注册到 ` }),
            new TextRun({ text: `biz/router/register.go`, font: "Consolas" }),
            new TextRun({ text: ` 的 ` }),
            new TextRun({ text: `GeneratedRegister()`, font: "Consolas" }),
            new TextRun({ text: ` 中。` })]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_2,
        outlineLevel: 1,
        spacing: { before: 180, after: 180 },
        children: [new TextRun({ text: `九、环境变量`, bold: true, size: 28, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Table({
        width: { size: 9024, type: WidthType.DXA },
        columnWidths: [3008, 3008, 3008],
        alignment: AlignmentType.CENTER,
        rows: [
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `变量`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `值`, bold: true })] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            shading: { fill: "D5E8F0", type: ShadingType.CLEAR },
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun({ text: `说明`, bold: true })] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`GOPROXY`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`https://goproxy.cn,direct`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`Go 模块代理（国内加速）`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`GOPATH`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`C:\\Users\\redmi\\go`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`Go 工作目录`)] })]
          })
          ]
        }),
        new TableRow({
          children: [
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`GOROOT`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`C:\\Program Files\\Go`)] })]
          }),
          new TableCell({
            width: { size: 3008, type: WidthType.DXA },
            borders: cellBorders,
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [new TextRun(`Go 安装目录`)] })]
          })
          ]
        })
        ]
      }),
      new Paragraph({
        heading: HeadingLevel.HEADING_2,
        outlineLevel: 1,
        spacing: { before: 180, after: 180 },
        children: [new TextRun({ text: `十、注意事项`, bold: true, size: 28, color: "1A5276", font: "Microsoft YaHei" })]
      }),
      new Paragraph({
        numbering: { reference: "num-list-1", level: 0 },
        children: [        new TextRun({ text: `router_gen.go`, font: "Consolas" }),
            new TextRun({ text: ` 和 ` }),
            new TextRun({ text: `biz/router/register.go`, font: "Consolas" }),
            new TextRun({ text: ` 标注了 ` }),
            new TextRun({ text: `DO NOT EDIT`, font: "Consolas" }),
            new TextRun({ text: `，不要手动修改，` }),
            new TextRun({ text: `hz`, font: "Consolas" }),
            new TextRun({ text: ` 重新生成时会覆盖` })]
      }),
      new Paragraph({
        numbering: { reference: "num-list-1", level: 0 },
        children: [        new TextRun({ text: `自定义路由统一写在 ` }),
            new TextRun({ text: `router.go`, font: "Consolas" }),
            new TextRun({ text: ` 的 ` }),
            new TextRun({ text: `customizedRegister()`, font: "Consolas" }),
            new TextRun({ text: ` 中` })]
      }),
      new Paragraph({
        numbering: { reference: "num-list-1", level: 0 },
        children: [        new TextRun({ text: `.gitignore`, font: "Consolas" }),
            new TextRun({ text: ` 已配置忽略 ` }),
            new TextRun({ text: `*.exe`, font: "Consolas" }),
            new TextRun({ text: `，编译产物不会被提交` })]
      }),
      new Paragraph({
        numbering: { reference: "num-list-1", level: 0 },
        children: [new TextRun({ text: `新终端窗口执行 make 前需确认 scoop 路径已在 PATH 中` })]
      }),
      new Paragraph({
        numbering: { reference: "num-list-1", level: 0 },
        children: [new TextRun({ text: `kill 服务可通过查找 8888 端口占用进程并终止` })]
      })
    ]
  }]
});

Packer.toBuffer(doc).then(buffer => fs.writeFileSync("D:/swallow/Swallow项目配置文档.docx", buffer));
