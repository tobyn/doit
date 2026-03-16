package dev.tobyn.doit.jetbrains

import com.intellij.execution.Executor
import com.intellij.execution.configurations.*
import com.intellij.execution.process.ProcessHandler
import com.intellij.execution.process.ProcessHandlerFactory
import com.intellij.execution.process.ProcessTerminatedListener
import com.intellij.execution.runners.ExecutionEnvironment
import com.intellij.openapi.options.SettingsEditor
import com.intellij.openapi.project.Project
import org.jdom.Element

class DoitCompileRunConfiguration(
    project: Project,
    factory: ConfigurationFactory,
    name: String
) : RunConfigurationBase<RunProfileState>(project, factory, name) {

    var filePath: String = ""
    var behaviorId: String = ""
    var copyToClipboard: Boolean = false
    var jsonOutput: Boolean = false
    var warningsAsErrors: Boolean = false
    var releaseMode: Boolean = false
    var locale: String = ""
    var outputPath: String = ""

    override fun getConfigurationEditor(): SettingsEditor<out RunConfiguration> =
        DoitCompileConfigurationEditor()

    override fun getState(executor: Executor, environment: ExecutionEnvironment): RunProfileState =
        DoitCompileRunState(this, environment)

    override fun readExternal(element: Element) {
        super.readExternal(element)
        filePath = element.getAttributeValue("filePath") ?: ""
        behaviorId = element.getAttributeValue("behaviorId") ?: ""
        copyToClipboard = element.getAttributeValue("copyToClipboard") != "false"
        jsonOutput = element.getAttributeValue("jsonOutput") == "true"
        warningsAsErrors = element.getAttributeValue("warningsAsErrors") == "true"
        releaseMode = element.getAttributeValue("releaseMode") == "true"
        locale = element.getAttributeValue("locale") ?: ""
        outputPath = element.getAttributeValue("outputPath") ?: ""
    }

    override fun writeExternal(element: Element) {
        super.writeExternal(element)
        element.setAttribute("filePath", filePath)
        element.setAttribute("behaviorId", behaviorId)
        element.setAttribute("copyToClipboard", copyToClipboard.toString())
        element.setAttribute("jsonOutput", jsonOutput.toString())
        element.setAttribute("warningsAsErrors", warningsAsErrors.toString())
        element.setAttribute("releaseMode", releaseMode.toString())
        element.setAttribute("locale", locale)
        element.setAttribute("outputPath", outputPath)
    }
}

private class DoitCompileRunState(
    private val config: DoitCompileRunConfiguration,
    environment: ExecutionEnvironment
) : CommandLineState(environment) {

    override fun startProcess(): ProcessHandler {
        val binary = DoitSettings.getInstance().effectiveBinaryPath()
        val cmd = GeneralCommandLine(binary, "compile")

        if (config.behaviorId.isNotBlank()) {
            cmd.addParameters("-b", config.behaviorId)
        }
        if (config.copyToClipboard) {
            cmd.addParameter("--copy")
        }
        if (config.jsonOutput) {
            cmd.addParameter("--json")
        }
        if (config.warningsAsErrors) {
            cmd.addParameter("--error")
        }
        if (config.releaseMode) {
            cmd.addParameter("--release")
        }
        if (config.locale.isNotBlank()) {
            cmd.addParameters("-l", config.locale)
        }
        if (config.outputPath.isNotBlank()) {
            cmd.addParameters("-o", config.outputPath)
        }
        if (config.filePath.isNotBlank()) {
            cmd.addParameter(config.filePath)
        }

        cmd.withCharset(Charsets.UTF_8)

        val handler = ProcessHandlerFactory.getInstance().createColoredProcessHandler(cmd)
        ProcessTerminatedListener.attach(handler)
        return handler
    }
}
