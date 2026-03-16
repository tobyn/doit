package dev.tobyn.doit.jetbrains

import com.intellij.execution.ProgramRunnerUtil
import com.intellij.execution.RunManager
import com.intellij.execution.executors.DefaultRunExecutor
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.ui.Messages

class DoitCompileBehaviorAction : AnAction() {

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return
        val vFile = e.getData(CommonDataKeys.VIRTUAL_FILE) ?: return

        val filePath = vFile.path
        val text = editor.document.text
        val offset = editor.caretModel.offset

        val behaviorId = findBehaviorAtCursor(text, offset)
        if (behaviorId == null) {
            Messages.showWarningDialog(
                project,
                "Could not determine which behavior to compile. " +
                    "Place the cursor inside a behavior declaration.",
                "doit Compile"
            )
            return
        }

        val runManager = RunManager.getInstance(project)

        // Find existing config for this file + behavior, or create one.
        val existing = runManager.allConfigurationsList
            .filterIsInstance<DoitCompileRunConfiguration>()
            .find { it.filePath == filePath && it.behaviorId == behaviorId }

        val settings = if (existing != null) {
            runManager.findSettings(existing)!!
        } else {
            val name = "${vFile.nameWithoutExtension}: $behaviorId"
            val settings = runManager.createConfiguration(
                name, DoitCompileConfigurationType::class.java
            )
            val config = settings.configuration as DoitCompileRunConfiguration
            config.filePath = filePath
            config.behaviorId = behaviorId
            runManager.addConfiguration(settings)
            settings
        }

        runManager.selectedConfiguration = settings
        ProgramRunnerUtil.executeConfiguration(settings, DefaultRunExecutor.getRunExecutorInstance())
    }

    override fun update(e: AnActionEvent) {
        val vFile = e.getData(CommonDataKeys.VIRTUAL_FILE)
        e.presentation.isEnabledAndVisible =
            vFile != null && vFile.extension == "doit" && e.project != null
    }

    companion object {
        /**
         * Finds the behavior ID at the given cursor offset by scanning for
         * `behavior <id> {` declarations and tracking brace depth.
         *
         * Returns null if:
         * - The file has no behaviors
         * - The file has multiple behaviors and the cursor is not inside one
         *
         * Returns the single behavior ID if the file has exactly one.
         */
        fun findBehaviorAtCursor(text: String, cursorOffset: Int): String? {
            val behaviors = parseBehaviorRanges(text)
            if (behaviors.isEmpty()) return null
            if (behaviors.size == 1) return behaviors[0].id

            // Multiple behaviors — find which one the cursor is inside.
            return behaviors.find { cursorOffset >= it.start && cursorOffset <= it.end }?.id
        }

        private data class BehaviorRange(val id: String, val start: Int, val end: Int)

        private fun parseBehaviorRanges(text: String): List<BehaviorRange> {
            val behaviors = mutableListOf<BehaviorRange>()
            var i = 0
            val len = text.length

            while (i < len) {
                val c = text[i]

                // Skip comments.
                if (c == '#') {
                    while (i < len && text[i] != '\n') i++
                    continue
                }

                // Skip strings.
                if (c == '"') {
                    i++
                    while (i < len && text[i] != '"') {
                        if (text[i] == '\\' && i + 1 < len) i++
                        i++
                    }
                    if (i < len) i++
                    continue
                }

                // Look for "behavior" keyword.
                if (c == 'b' && text.startsWith("behavior", i) &&
                    (i == 0 || !isIdentChar(text[i - 1])) &&
                    i + 8 < len && !isIdentChar(text[i + 8])
                ) {
                    val declStart = i
                    i += 8

                    // Skip whitespace.
                    while (i < len && text[i].isWhitespace()) i++

                    // Read behavior ID (identifier or string).
                    val id: String
                    if (i < len && text[i] == '"') {
                        i++
                        val idStart = i
                        while (i < len && text[i] != '"') {
                            if (text[i] == '\\' && i + 1 < len) i++
                            i++
                        }
                        id = text.substring(idStart, i)
                        if (i < len) i++
                    } else {
                        val idStart = i
                        while (i < len && isIdentChar(text[i])) i++
                        if (i == idStart) { i++; continue }
                        id = text.substring(idStart, i)
                    }

                    // Skip to opening brace.
                    while (i < len && text[i] != '{') i++
                    if (i >= len) continue
                    i++ // skip '{'

                    // Track brace depth to find the end.
                    var depth = 1
                    while (i < len && depth > 0) {
                        when (text[i]) {
                            '{' -> depth++
                            '}' -> depth--
                            '#' -> { while (i < len && text[i] != '\n') i++ }
                            '"' -> {
                                i++
                                while (i < len && text[i] != '"') {
                                    if (text[i] == '\\' && i + 1 < len) i++
                                    i++
                                }
                            }
                        }
                        i++
                    }

                    behaviors.add(BehaviorRange(id, declStart, i))
                    continue
                }

                i++
            }

            return behaviors
        }

        private fun isIdentChar(c: Char): Boolean =
            c in 'a'..'z' || c in 'A'..'Z' || c in '0'..'9' || c == '_'
    }
}
