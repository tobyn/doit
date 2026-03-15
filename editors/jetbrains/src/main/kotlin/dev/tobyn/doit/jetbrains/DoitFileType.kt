package dev.tobyn.doit.jetbrains

import com.intellij.lang.Language
import com.intellij.openapi.fileTypes.LanguageFileType
import com.intellij.openapi.util.IconLoader
import org.jetbrains.plugins.textmate.TextMateBackedFileType
import javax.swing.Icon

object DoitLanguage : Language("doit")

class DoitFileType private constructor() : LanguageFileType(DoitLanguage), TextMateBackedFileType {
    override fun getName(): String = "doit"
    override fun getDescription(): String = "doit language file"
    override fun getDefaultExtension(): String = "doit"
    override fun getIcon(): Icon = FILE_ICON

    companion object {
        @JvmField
        val INSTANCE = DoitFileType()
        private val FILE_ICON = IconLoader.getIcon("/icons/doit-file.svg", DoitFileType::class.java)
    }
}
