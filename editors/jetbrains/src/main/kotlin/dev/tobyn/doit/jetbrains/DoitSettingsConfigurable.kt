package dev.tobyn.doit.jetbrains

import com.intellij.openapi.fileChooser.FileChooserDescriptorFactory
import com.intellij.openapi.options.Configurable
import com.intellij.openapi.project.ProjectManager
import com.intellij.openapi.ui.TextFieldWithBrowseButton
import com.intellij.util.ui.FormBuilder
import com.redhat.devtools.lsp4ij.LanguageServerManager
import javax.swing.JComponent
import javax.swing.JPanel

class DoitSettingsConfigurable : Configurable {
    private var panel: JPanel? = null
    private var binaryPathField: TextFieldWithBrowseButton? = null

    override fun getDisplayName(): String = "doit Language"

    override fun createComponent(): JComponent {
        val field = TextFieldWithBrowseButton().apply {
            addBrowseFolderListener(
                "Select doit Binary", null, null,
                FileChooserDescriptorFactory.createSingleFileDescriptor()
            )
        }
        binaryPathField = field

        val form = FormBuilder.createFormBuilder()
            .addLabeledComponent("doit binary path:", field)
            .addComponentFillVertically(JPanel(), 0)
            .panel
        panel = form
        reset()
        return form
    }

    override fun isModified(): Boolean {
        val settings = DoitSettings.getInstance()
        return binaryPathField?.text != settings.binaryPath
    }

    override fun apply() {
        DoitSettings.getInstance().binaryPath = binaryPathField?.text ?: ""

        // Restart the LSP server in all open projects so the new binary takes effect.
        for (project in ProjectManager.getInstance().openProjects) {
            LanguageServerManager.getInstance(project).start("dev.tobyn.doit.lsp")
        }
    }

    override fun reset() {
        binaryPathField?.text = DoitSettings.getInstance().binaryPath
    }

    override fun disposeUIResources() {
        panel = null
        binaryPathField = null
    }
}
