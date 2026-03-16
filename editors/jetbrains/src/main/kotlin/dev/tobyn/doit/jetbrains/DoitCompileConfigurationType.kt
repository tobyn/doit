package dev.tobyn.doit.jetbrains

import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.execution.configurations.ConfigurationType
import com.intellij.execution.configurations.RunConfiguration
import com.intellij.openapi.project.Project
import javax.swing.Icon

class DoitCompileConfigurationType : ConfigurationType {
    override fun getDisplayName(): String = "doit Compile"
    override fun getConfigurationTypeDescription(): String = "Compile a doit behavior"
    override fun getIcon(): Icon = DoitFileType.INSTANCE.icon
    override fun getId(): String = ID

    override fun getConfigurationFactories(): Array<ConfigurationFactory> =
        arrayOf(DoitCompileConfigurationFactory(this))

    companion object {
        const val ID = "DoitCompileConfiguration"
    }
}

class DoitCompileConfigurationFactory(type: ConfigurationType) : ConfigurationFactory(type) {
    override fun getId(): String = DoitCompileConfigurationType.ID

    override fun createTemplateConfiguration(project: Project): RunConfiguration =
        DoitCompileRunConfiguration(project, this, "doit Compile")
}
