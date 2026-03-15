package dev.tobyn.doit.jetbrains

import com.intellij.openapi.diagnostic.Logger
import com.intellij.openapi.project.Project
import com.intellij.psi.PsiFile
import com.redhat.devtools.lsp4ij.LanguageServerFactory
import com.redhat.devtools.lsp4ij.client.features.LSPClientFeatures
import com.redhat.devtools.lsp4ij.client.features.LSPFormattingFeature
import com.redhat.devtools.lsp4ij.server.ProcessStreamConnectionProvider
import com.redhat.devtools.lsp4ij.server.StreamConnectionProvider

private val LOG = Logger.getInstance("dev.tobyn.doit.lsp")

class DoitLspServerFactory : LanguageServerFactory {
    override fun createConnectionProvider(project: Project): StreamConnectionProvider {
        LOG.warn("createConnectionProvider called for project: ${project.name}")
        return DoitLanguageServer()
    }

    override fun createClientFeatures(): LSPClientFeatures {
        LOG.warn("createClientFeatures called")
        return LSPClientFeatures()
            .setFormattingFeature(DoitFormattingFeature())
    }
}

private class DoitFormattingFeature : LSPFormattingFeature() {
    override fun isExistingFormatterOverrideable(file: PsiFile): Boolean = true
}

private class DoitLanguageServer : ProcessStreamConnectionProvider() {
    init {
        val binary = DoitSettings.getInstance().effectiveBinaryPath()
        LOG.warn("DoitLanguageServer init: binary=$binary")
        commands = listOf(binary, "language-server")
        LOG.warn("DoitLanguageServer commands set: $commands")
    }
}
