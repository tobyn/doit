package dev.tobyn.doit.jetbrains

import com.intellij.openapi.project.Project
import com.redhat.devtools.lsp4ij.LanguageServerFactory
import com.redhat.devtools.lsp4ij.server.ProcessStreamConnectionProvider
import com.redhat.devtools.lsp4ij.server.StreamConnectionProvider

class DoitLspServerFactory : LanguageServerFactory {
    override fun createConnectionProvider(project: Project): StreamConnectionProvider {
        return DoitLanguageServer()
    }
}

private class DoitLanguageServer : ProcessStreamConnectionProvider() {
    init {
        val binary = DoitSettings.getInstance().effectiveBinaryPath()
        commands = listOf(binary, "language-server")
    }
}
