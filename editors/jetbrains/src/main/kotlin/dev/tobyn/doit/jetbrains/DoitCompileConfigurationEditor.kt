package dev.tobyn.doit.jetbrains

import com.intellij.openapi.fileChooser.FileChooserDescriptorFactory
import com.intellij.openapi.options.SettingsEditor
import com.intellij.openapi.ui.TextFieldWithBrowseButton
import com.intellij.ui.components.JBCheckBox
import com.intellij.ui.components.JBTextField
import com.intellij.util.ui.FormBuilder
import javax.swing.JComponent
import javax.swing.JPanel

class DoitCompileConfigurationEditor : SettingsEditor<DoitCompileRunConfiguration>() {

    private val filePathField = TextFieldWithBrowseButton().apply {
        addBrowseFolderListener(
            "Select doit Source File", null, null,
            FileChooserDescriptorFactory.createSingleFileDescriptor("doit")
        )
    }
    private val behaviorIdField = JBTextField()
    private val copyToClipboardBox = JBCheckBox("Copy output to clipboard")
    private val jsonOutputBox = JBCheckBox("JSON output (instead of Base62)")
    private val warningsAsErrorsBox = JBCheckBox("Treat warnings as errors")
    private val releaseModeBox = JBCheckBox("Release mode (omit asserts)")
    private val localeField = JBTextField()
    private val outputPathField = TextFieldWithBrowseButton().apply {
        addBrowseFolderListener(
            "Select Output File", null, null,
            FileChooserDescriptorFactory.createSingleFileNoJarsDescriptor()
        )
    }

    private val panel: JPanel = FormBuilder.createFormBuilder()
        .addLabeledComponent("Source file:", filePathField)
        .addLabeledComponent("Behavior ID:", behaviorIdField)
        .addComponent(copyToClipboardBox)
        .addComponent(jsonOutputBox)
        .addComponent(warningsAsErrorsBox)
        .addComponent(releaseModeBox)
        .addLabeledComponent("Locale:", localeField)
        .addLabeledComponent("Output file:", outputPathField)
        .addComponentFillVertically(JPanel(), 0)
        .panel

    override fun resetEditorFrom(config: DoitCompileRunConfiguration) {
        filePathField.text = config.filePath
        behaviorIdField.text = config.behaviorId
        copyToClipboardBox.isSelected = config.copyToClipboard
        jsonOutputBox.isSelected = config.jsonOutput
        warningsAsErrorsBox.isSelected = config.warningsAsErrors
        releaseModeBox.isSelected = config.releaseMode
        localeField.text = config.locale
        outputPathField.text = config.outputPath
    }

    override fun applyEditorTo(config: DoitCompileRunConfiguration) {
        config.filePath = filePathField.text
        config.behaviorId = behaviorIdField.text
        config.copyToClipboard = copyToClipboardBox.isSelected
        config.jsonOutput = jsonOutputBox.isSelected
        config.warningsAsErrors = warningsAsErrorsBox.isSelected
        config.releaseMode = releaseModeBox.isSelected
        config.locale = localeField.text
        config.outputPath = outputPathField.text
    }

    override fun createEditor(): JComponent = panel
}
