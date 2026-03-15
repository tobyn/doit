plugins {
    id("org.jetbrains.kotlin.jvm") version "1.9.25"
    id("org.jetbrains.intellij.platform") version "2.5.0"
}

group = "dev.tobyn.doit"
version = providers.gradleProperty("pluginVersion").get()

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        intellijIdeaCommunity(providers.gradleProperty("platformVersion").get())
        bundledPlugin("org.jetbrains.plugins.textmate")
        plugin("com.redhat.devtools.lsp4ij", providers.gradleProperty("lsp4ijVersion").get())
        pluginVerifier()
    }
}

intellijPlatform {
    pluginConfiguration {
        id = "dev.tobyn.doit"
        name = "doit Language"
        version = providers.gradleProperty("pluginVersion").get()
        description = """
            Support for the <a href="https://github.com/tobyn/doit">doit</a>
            programming language, which targets
            <a href="https://www.desyncedgame.com/">Desynced</a> behavior
            controllers.

            <ul>
              <li>Syntax highlighting via TextMate grammar</li>
              <li>Semantic token highlighting via built-in LSP server</li>
              <li>Configurable path to the <code>doit</code> binary</li>
            </ul>
        """.trimIndent()
        vendor {
            name = "Tobyn Baugher"
        }
        ideaVersion {
            sinceBuild = "242"
            untilBuild = provider { null }
        }
    }
    pluginVerification {
        ides {
            recommended()
        }
    }
}

kotlin {
    jvmToolchain(17)
}

// Copy the TextMate bundle into the plugin sandbox as a top-level directory
// (not inside a jar). The TextMate plugin needs real files on disk.
val pluginName = intellijPlatform.projectName

tasks.named("prepareSandbox") {
    doLast {
        val sandboxDir = layout.buildDirectory
            .dir("idea-sandbox/IC-${providers.gradleProperty("platformVersion").get()}/plugins")
            .get().asFile
        val textmateDir = sandboxDir.resolve("${pluginName.get()}/textmate")
        textmateDir.mkdirs()
        file("../doit.tmLanguage.json").copyTo(textmateDir.resolve("doit.tmLanguage.json"), overwrite = true)
        file("src/main/resources/textmate/package.json").copyTo(textmateDir.resolve("package.json"), overwrite = true)
        file("src/main/resources/textmate/language-configuration.json").copyTo(textmateDir.resolve("language-configuration.json"), overwrite = true)
    }
}
