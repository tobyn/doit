---
layout: home
hero:
  name: doit
  text: A language for Desynced behaviors
  tagline: Write behavior controllers in a real programming language. Compile to Base62 strings the game understands.
  actions:
    - theme: brand
      text: Get Started
      link: /docs/
    - theme: alt
      text: Download
      link: /downloads
features:
  - title: Real Language
    details: Variables, functions, control flow, imports — write behaviors with the tools you expect from a programming language.
  - title: Compiles to Desynced
    details: The doit compiler produces the same Base62 strings the game uses for import/export. Paste them in and go.
  - title: Editor Support
    details: VS Code and JetBrains extensions with syntax highlighting, diagnostics, formatting, and hover docs.
---

<script setup>
import DoitPlayground from './.vitepress/theme/DoitPlayground.vue'
</script>

## Try it

<DoitPlayground />
