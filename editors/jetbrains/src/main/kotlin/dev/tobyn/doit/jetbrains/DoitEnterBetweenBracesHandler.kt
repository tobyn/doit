package dev.tobyn.doit.jetbrains

import com.intellij.codeInsight.editorActions.enter.EnterHandlerDelegate
import com.intellij.codeInsight.editorActions.enter.EnterHandlerDelegateAdapter
import com.intellij.openapi.actionSystem.DataContext
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.editor.actionSystem.EditorActionHandler
import com.intellij.openapi.util.Key
import com.intellij.openapi.util.Ref
import com.intellij.psi.PsiFile

/**
 * Splits closing braces onto their own line when Enter is pressed between { and }.
 *
 * After the default Enter handler runs, the document looks like:
 *     if a {
 *         }
 * with the cursor before }. This handler detects that pattern in preprocessEnter,
 * removes the }, and re-inserts it on its own line in postProcessEnter:
 *     if a {
 *         |
 *     }
 */
class DoitEnterBetweenBracesHandler : EnterHandlerDelegateAdapter() {

    override fun preprocessEnter(
        file: PsiFile,
        editor: Editor,
        caretOffset: Ref<Int>,
        caretAdvance: Ref<Int>,
        dataContext: DataContext,
        originalHandler: EditorActionHandler?
    ): EnterHandlerDelegate.Result {
        if (file.fileType.name != "doit") return EnterHandlerDelegate.Result.Continue

        val document = editor.document
        val text = document.charsSequence
        val offset = caretOffset.get()

        // Find the closing brace immediately after the cursor (skip whitespace on same line)
        var braceOffset = -1
        for (i in offset until text.length) {
            val ch = text[i]
            if (ch == '}') {
                braceOffset = i
                break
            }
            if (ch == '\n' || !ch.isWhitespace()) break
        }
        if (braceOffset < 0) return EnterHandlerDelegate.Result.Continue

        // Check that the line ends with { before the cursor (skip whitespace)
        var foundBrace = false
        for (i in (offset - 1) downTo 0) {
            val ch = text[i]
            if (ch == '{') {
                foundBrace = true
                break
            }
            if (ch == '\n' || !ch.isWhitespace()) break
        }
        if (!foundBrace) return EnterHandlerDelegate.Result.Continue

        // Compute the indent of the current line (the line containing {)
        val lineNumber = document.getLineNumber(offset)
        val lineStart = document.getLineStartOffset(lineNumber)
        val baseIndent = buildString {
            for (i in lineStart until text.length) {
                val ch = text[i]
                if (ch == ' ' || ch == '\t') append(ch) else break
            }
        }

        // Remove the } now; postProcessEnter will re-insert it on its own line
        // after the default Enter handler has placed the cursor.
        document.deleteString(braceOffset, braceOffset + 1)
        editor.putUserData(PENDING_CLOSE_BRACE, baseIndent)

        return EnterHandlerDelegate.Result.Continue
    }

    override fun postProcessEnter(
        file: PsiFile,
        editor: Editor,
        dataContext: DataContext
    ): EnterHandlerDelegate.Result {
        val baseIndent = editor.getUserData(PENDING_CLOSE_BRACE)
            ?: return EnterHandlerDelegate.Result.Continue
        editor.putUserData(PENDING_CLOSE_BRACE, null)

        val document = editor.document
        val caretOffset = editor.caretModel.offset
        document.insertString(caretOffset, "\n$baseIndent}")

        return EnterHandlerDelegate.Result.Continue
    }

    companion object {
        private val PENDING_CLOSE_BRACE = Key.create<String>("doit.pendingCloseBrace")
    }
}
