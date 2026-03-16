import { defineConfig } from "vitepress";
import { readFileSync } from "fs";
import { resolve } from "path";

const doitGrammar = JSON.parse(
  readFileSync(resolve(__dirname, "../../editors/doit.tmLanguage.json"), "utf-8")
);

export default defineConfig({
  title: "doit",
  description:
    "A programming language for Desynced behavior controllers",

  cleanUrls: true,

  head: [
    ["meta", { name: "theme-color", content: "#1a1a2e" }],
  ],

  markdown: {
    languages: [
      {
        ...doitGrammar,
        name: "doit",
      },
    ],
  },

  themeConfig: {
    nav: [
      { text: "Docs", link: "/docs/" },
      { text: "Downloads", link: "/downloads" },
    ],

    sidebar: {
      "/docs/": [
        {
          text: "Documentation",
          items: [
            { text: "Overview", link: "/docs/" },
            { text: "Language", link: "/docs/language" },
            { text: "Functions", link: "/docs/functions" },
            { text: "instruction Intrinsic", link: "/docs/instruction" },
            { text: "Toolchain", link: "/docs/toolchain" },
          ],
        },
      ],
    },

    socialLinks: [
      { icon: "github", link: "https://github.com/tobyn/doit" },
    ],

    search: {
      provider: "local",
    },
  },
});
