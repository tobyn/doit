package dev.tobyn.doit.jetbrains

import com.intellij.openapi.project.Project
import com.intellij.psi.PsiFile
import com.redhat.devtools.lsp4ij.LanguageServerEnablementSupport
import com.redhat.devtools.lsp4ij.LanguageServerFactory
import com.redhat.devtools.lsp4ij.client.features.LSPClientFeatures
import com.redhat.devtools.lsp4ij.client.features.LSPFormattingFeature
import com.redhat.devtools.lsp4ij.server.ProcessStreamConnectionProvider
import com.redhat.devtools.lsp4ij.server.StreamConnectionProvider
import java.io.File

class DoitLspServerFactory : LanguageServerFactory, LanguageServerEnablementSupport {
    override fun createConnectionProvider(project: Project): StreamConnectionProvider {
        return DoitLanguageServer()
    }

    override fun createClientFeatures(): LSPClientFeatures {
        return LSPClientFeatures()
            .setFormattingFeature(DoitFormattingFeature())
    }

    override fun isEnabled(project: Project): Boolean {
        val settings = DoitSettings.getInstance()
        if (settings.binaryPath.isNotBlank()) return true
        return isBinaryOnPath("doit")
    }

    override fun setEnabled(enabled: Boolean, project: Project) {
        // Computed dynamically from settings — no-op
    }
}

private class DoitFormattingFeature : LSPFormattingFeature() {
    override fun isExistingFormatterOverrideable(file: PsiFile): Boolean = true
}

private class DoitLanguageServer : ProcessStreamConnectionProvider() {
    init {
        val binary = DoitSettings.getInstance().effectiveBinaryPath()
        commands = listOf(binary, "language-server")
    }
}

/** Checks whether a binary with the given name exists on the system PATH. */
internal fun isBinaryOnPath(name: String): Boolean {
    val pathDirs = System.getenv("PATH")?.split(File.pathSeparator) ?: return false
    val isWindows = System.getProperty("os.name").lowercase().contains("win")
    val candidates = if (isWindows) {
        listOf("$name.exe", "$name.cmd", "$name.bat", name)
    } else {
        listOf(name)
    }
    return pathDirs.any { dir ->
        candidates.any { File(dir, it).canExecute() }
    }
}
