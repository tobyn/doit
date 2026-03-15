package dev.tobyn.doit.jetbrains

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.Service
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage

@Service
@State(name = "DoitSettings", storages = [Storage("doit.xml")])
class DoitSettings : PersistentStateComponent<DoitSettings.State> {
    data class State(var binaryPath: String = "")

    private var state = State()

    override fun getState(): State = state
    override fun loadState(state: State) {
        this.state = state
    }

    var binaryPath: String
        get() = state.binaryPath
        set(value) { state.binaryPath = value }

    /** Returns the binary path to use: user-configured or "doit" for PATH lookup. */
    fun effectiveBinaryPath(): String = binaryPath.ifBlank { "doit" }

    companion object {
        fun getInstance(): DoitSettings =
            ApplicationManager.getApplication().getService(DoitSettings::class.java)
    }
}
