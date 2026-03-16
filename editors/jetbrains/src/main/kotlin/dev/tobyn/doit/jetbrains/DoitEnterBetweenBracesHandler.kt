package dev.tobyn.doit.jetbrains

import com.intellij.openapi.actionSystem.DataContext
import com.intellij.openapi.actionSystem.IdeActions
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.editor.Caret
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.editor.actionSystem.EditorActionHandler
import com.intellij.openapi.editor.actionSystem.EditorActionManager
import com.intellij.openapi.fileEditor.FileDocumentManager
import com.intellij.openapi.project.Project
import com.intellij.openapi.startup.ProjectActivity

/**
 * Wraps the editor Enter action to split closing braces onto their own line
 * when Enter is pressed between { and } in .doit files.
 *
 * EnterHandlerDelegate is not invoked for TextMate-backed file types, so we
 * wrap the Enter action handler directly via EditorActionManager instead.
 *
 * When the LSP is active, this handler defers to the LSP formatter (which
 * handles brace-splitting correctly but with a slight delay). Modifying the
 * document here would conflict with the LSP's asynchronous formatting response.
 */
class DoitEnterHandlerInstaller : ProjectActivity {
    override suspend fun execute(project: Project) {
        val manager = EditorActionManager.getInstance()
        val original = manager.getActionHandler(IdeActions.ACTION_EDITOR_ENTER)
        if (original !is DoitEnterActionHandler) {
            manager.setActionHandler(
                IdeActions.ACTION_EDITOR_ENTER,
                DoitEnterActionHandler(original)
            )
        }
    }
}

class DoitEnterActionHandler(private val original: EditorActionHandler) : EditorActionHandler() {
    override fun doExecute(editor: Editor, caret: Caret?, dataContext: DataContext) {
        val file = FileDocumentManager.getInstance().getFile(editor.document)
        if (file?.extension != "doit") {
            original.execute(editor, caret, dataContext)
            return
        }

        val document = editor.document
        val text = document.charsSequence
        val offset = editor.caretModel.offset

        // Detect cursor between { and } (possibly with whitespace)
        var closingBraceAfter = false
        var openingBraceBefore = false

        // Look forward for }
        for (i in offset until text.length) {
            val ch = text[i]
            if (ch == '}') { closingBraceAfter = true; break }
            if (ch == '\n' || !ch.isWhitespace()) break
        }
        // Look backward for {
        for (i in (offset - 1) downTo 0) {
            val ch = text[i]
            if (ch == '{') { openingBraceBefore = true; break }
            if (ch == '\n' || !ch.isWhitespace()) break
        }

        if (!(openingBraceBefore && closingBraceAfter)) {
            original.execute(editor, caret, dataContext)
            return
        }

        // Compute the base indent (indent of the line containing {)
        val lineNumber = document.getLineNumber(offset)
        val lineStart = document.getLineStartOffset(lineNumber)
        val baseIndent = buildString {
            for (i in lineStart until text.length) {
                val ch = text[i]
                if (ch == ' ' || ch == '\t') append(ch) else break
            }
        }

        // Run the original Enter handler (inserts newline + indent for cursor)
        original.execute(editor, caret, dataContext)

        // Now insert a newline + base indent before the } that followed the cursor
        val newOffset = editor.caretModel.offset
        val newText = document.charsSequence

        // Find the } after the new cursor position
        var braceOffset = -1
        for (i in newOffset until newText.length) {
            val ch = newText[i]
            if (ch == '}') { braceOffset = i; break }
            if (ch == '\n' || !ch.isWhitespace()) break
        }

        if (braceOffset >= 0) {
            ApplicationManager.getApplication().runWriteAction {
                document.insertString(braceOffset, "\n$baseIndent")
            }
        }
    }

    public override fun isEnabledForCaret(editor: Editor, caret: Caret, dataContext: DataContext?): Boolean {
        return original.isEnabled(editor, caret, dataContext)
    }
}
