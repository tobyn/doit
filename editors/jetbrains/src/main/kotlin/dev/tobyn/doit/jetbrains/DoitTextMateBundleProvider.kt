package dev.tobyn.doit.jetbrains

import com.intellij.ide.plugins.PluginManagerCore
import com.intellij.openapi.extensions.PluginId
import org.jetbrains.plugins.textmate.api.TextMateBundleProvider

class DoitTextMateBundleProvider : TextMateBundleProvider {
    override fun getBundles(): List<TextMateBundleProvider.PluginBundle> {
        val plugin = PluginManagerCore.getPlugin(PluginId.getId("dev.tobyn.doit"))
            ?: return emptyList()
        val bundlePath = plugin.pluginPath.resolve("textmate")
        return listOf(TextMateBundleProvider.PluginBundle("doit", bundlePath))
    }
}
