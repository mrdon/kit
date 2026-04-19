//! WASM shim for the Monty Python interpreter.
//!
//! Exposes Monty's pause/resume API as C-ABI WASM exports for consumption
//! by Go via wazero. State (runners, snapshots) is stored in a global map
//! keyed by handles — safe because WASM is single-threaded and we create
//! one instance per Execute() call on the Go side.

use std::alloc::{alloc, dealloc, Layout};
use std::borrow::Cow;
use std::collections::HashMap;
use std::sync::Mutex;
use std::time::Duration;

use monty::{
    DictPairs, ExcType, ExtFunctionResult, FunctionCall, LimitedTracker, MontyException,
    MontyObject, MontyRun, NameLookupResult, OsCall, PrintWriter, PrintWriterCallback,
    ResolveFutures, ResourceLimits, RunProgress,
};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;

// ---------------------------------------------------------------------------
// MontyObject <-> serde_json::Value conversion
// ---------------------------------------------------------------------------

/// Convert MontyObject to a plain JSON value (not the tagged enum format).
fn monty_to_json(obj: &MontyObject) -> JsonValue {
    match obj {
        MontyObject::None => JsonValue::Null,
        MontyObject::Bool(b) => JsonValue::Bool(*b),
        MontyObject::Int(i) => serde_json::json!(*i),
        MontyObject::BigInt(bi) => {
            // Try to fit in i64, fall back to string
            if let Ok(v) = i64::try_from(bi) {
                serde_json::json!(v)
            } else {
                JsonValue::String(bi.to_string())
            }
        }
        MontyObject::Float(f) => serde_json::json!(*f),
        MontyObject::String(s) => JsonValue::String(s.clone()),
        MontyObject::Bytes(b) => {
            // Encode as array of ints
            JsonValue::Array(b.iter().map(|byte| serde_json::json!(*byte)).collect())
        }
        MontyObject::List(items) => {
            JsonValue::Array(items.iter().map(monty_to_json).collect())
        }
        MontyObject::Tuple(items) => {
            JsonValue::Array(items.iter().map(monty_to_json).collect())
        }
        MontyObject::Dict(pairs) => {
            let mut map = serde_json::Map::new();
            for (k, v) in pairs {
                let key = match k {
                    MontyObject::String(s) => s.clone(),
                    other => format!("{:?}", other),
                };
                map.insert(key, monty_to_json(v));
            }
            JsonValue::Object(map)
        }
        MontyObject::Set(items) | MontyObject::FrozenSet(items) => {
            JsonValue::Array(items.iter().map(monty_to_json).collect())
        }
        MontyObject::Ellipsis => JsonValue::String("...".to_owned()),
        MontyObject::Path(p) => JsonValue::String(p.clone()),
        MontyObject::Exception { exc_type, arg } => {
            let mut map = serde_json::Map::new();
            map.insert("exception".to_owned(), JsonValue::String(format!("{:?}", exc_type)));
            if let Some(a) = arg {
                map.insert("message".to_owned(), JsonValue::String(a.clone()));
            }
            JsonValue::Object(map)
        }
        MontyObject::NamedTuple {
            type_name,
            field_names,
            values,
        } => {
            let mut map = serde_json::Map::new();
            map.insert("__type__".to_owned(), JsonValue::String(type_name.clone()));
            for (name, val) in field_names.iter().zip(values.iter()) {
                map.insert(name.clone(), monty_to_json(val));
            }
            JsonValue::Object(map)
        }
        MontyObject::Dataclass {
            name,
            field_names,
            attrs,
            ..
        } => {
            let mut map = serde_json::Map::new();
            map.insert("__type__".to_owned(), JsonValue::String(name.clone()));
            for field_name in field_names {
                for (k, v) in attrs {
                    if let MontyObject::String(key) = k {
                        if key == field_name {
                            map.insert(field_name.clone(), monty_to_json(v));
                            break;
                        }
                    }
                }
            }
            JsonValue::Object(map)
        }
        // Fallback for remaining variants
        other => JsonValue::String(format!("{:?}", other)),
    }
}

/// Convert a plain JSON value to a MontyObject.
fn json_to_monty(val: &JsonValue) -> MontyObject {
    match val {
        JsonValue::Null => MontyObject::None,
        JsonValue::Bool(b) => MontyObject::Bool(*b),
        JsonValue::Number(n) => {
            if let Some(i) = n.as_i64() {
                MontyObject::Int(i)
            } else if let Some(f) = n.as_f64() {
                MontyObject::Float(f)
            } else {
                MontyObject::None
            }
        }
        JsonValue::String(s) => MontyObject::String(s.clone()),
        JsonValue::Array(items) => {
            MontyObject::List(items.iter().map(json_to_monty).collect())
        }
        JsonValue::Object(map) => {
            let pairs: Vec<(MontyObject, MontyObject)> = map
                .iter()
                .map(|(k, v)| (MontyObject::String(k.clone()), json_to_monty(v)))
                .collect();
            MontyObject::Dict(DictPairs::from(pairs))
        }
    }
}

fn monty_args_to_json(args: &[MontyObject]) -> JsonValue {
    JsonValue::Array(args.iter().map(monty_to_json).collect())
}

fn monty_kwargs_to_json(kwargs: &[(MontyObject, MontyObject)]) -> JsonValue {
    let mut map = serde_json::Map::new();
    for (k, v) in kwargs {
        let key = match k {
            MontyObject::String(s) => s.clone(),
            other => format!("{:?}", other),
        };
        map.insert(key, monty_to_json(v));
    }
    JsonValue::Object(map)
}

/// Merge positional args and kwargs into a single JSON object.
/// Positional args are mapped to parameter names by index.
fn merge_args(
    func_name: &str,
    args: &[MontyObject],
    kwargs: &[(MontyObject, MontyObject)],
    param_registry: &HashMap<String, Vec<String>>,
) -> JsonValue {
    let mut map = serde_json::Map::new();
    // Map positional args to parameter names.
    if let Some(param_names) = param_registry.get(func_name) {
        for (i, arg) in args.iter().enumerate() {
            if i < param_names.len() {
                map.insert(param_names[i].clone(), monty_to_json(arg));
            }
        }
    }
    // Merge kwargs (overrides positional — Python semantics).
    for (k, v) in kwargs {
        if let MontyObject::String(key) = k {
            map.insert(key.clone(), monty_to_json(v));
        }
    }
    JsonValue::Object(map)
}

/// External function definition with parameter names.
#[derive(Deserialize)]
struct ExtFuncDef {
    name: String,
    #[serde(default)]
    params: Vec<String>,
}

// ---------------------------------------------------------------------------
// Global state
// ---------------------------------------------------------------------------

static STATE: Mutex<Option<State>> = Mutex::new(None);
static RESULT_BUF: Mutex<Vec<u8>> = Mutex::new(Vec::new());
static PARAM_REGISTRY: Mutex<Option<HashMap<String, Vec<String>>>> = Mutex::new(None);

struct State {
    next_id: u32,
    runners: HashMap<u32, MontyRun>,
    snapshots: HashMap<u32, SnapshotState>,
}

enum SnapshotState {
    FunctionCall(FunctionCall<LimitedTracker>),
    OsCall(OsCall<LimitedTracker>),
    ResolveFutures(ResolveFutures<LimitedTracker>),
}

impl State {
    fn new() -> Self {
        Self {
            next_id: 1,
            runners: HashMap::new(),
            snapshots: HashMap::new(),
        }
    }

    fn next_handle(&mut self) -> u32 {
        let id = self.next_id;
        self.next_id += 1;
        id
    }
}

fn with_state<F, R>(f: F) -> R
where
    F: FnOnce(&mut State) -> R,
{
    let mut guard = STATE.lock().unwrap();
    let state = guard.get_or_insert_with(State::new);
    f(state)
}

fn with_param_registry<F, R>(f: F) -> R
where
    F: FnOnce(&HashMap<String, Vec<String>>) -> R,
{
    let guard = PARAM_REGISTRY.lock().unwrap();
    static EMPTY: std::sync::LazyLock<HashMap<String, Vec<String>>> =
        std::sync::LazyLock::new(HashMap::new);
    let registry = guard.as_ref().unwrap_or(&EMPTY);
    f(registry)
}

// ---------------------------------------------------------------------------
// Result buffer helpers
// ---------------------------------------------------------------------------

fn set_result(data: &[u8]) {
    let mut buf = RESULT_BUF.lock().unwrap();
    buf.clear();
    buf.extend_from_slice(data);
}

fn set_result_json<T: Serialize>(value: &T) {
    let json = serde_json::to_vec(value).unwrap_or_default();
    set_result(&json);
}

// ---------------------------------------------------------------------------
// JSON wire types (using plain JSON values, not MontyObject tagged enums)
// ---------------------------------------------------------------------------

#[derive(Serialize)]
struct ProgressResult {
    status: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    value: Option<JsonValue>,
    #[serde(skip_serializing_if = "Option::is_none")]
    snapshot_handle: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    function_name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    os_function: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    args: Option<JsonValue>,
    #[serde(skip_serializing_if = "Option::is_none")]
    kwargs: Option<JsonValue>,
    #[serde(skip_serializing_if = "Option::is_none")]
    call_id: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    method_call: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pending_call_ids: Option<Vec<u32>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    print_output: Option<String>,
}

#[derive(Deserialize)]
struct LimitsInput {
    #[serde(default)]
    max_allocations: Option<usize>,
    #[serde(default)]
    max_duration_ms: Option<u64>,
    #[serde(default)]
    max_memory: Option<usize>,
    #[serde(default)]
    max_recursion_depth: Option<usize>,
}

// ---------------------------------------------------------------------------
// Print writer that collects output
// ---------------------------------------------------------------------------

struct CollectPrintWriter {
    buf: String,
}

impl CollectPrintWriter {
    fn new() -> Self {
        Self { buf: String::new() }
    }
}

impl PrintWriterCallback for CollectPrintWriter {
    fn stdout_write(&mut self, output: Cow<'_, str>) -> Result<(), MontyException> {
        self.buf.push_str(&output);
        Ok(())
    }

    fn stdout_push(&mut self, end: char) -> Result<(), MontyException> {
        self.buf.push(end);
        Ok(())
    }
}

// ---------------------------------------------------------------------------
// RunProgress -> ProgressResult conversion
// ---------------------------------------------------------------------------

fn progress_to_result(
    progress: RunProgress<LimitedTracker>,
    print_output: String,
    param_registry: &HashMap<String, Vec<String>>,
) -> ProgressResult {
    let print_out = if print_output.is_empty() {
        None
    } else {
        Some(print_output)
    };

    match progress {
        RunProgress::Complete(value) => ProgressResult {
            status: "complete",
            value: Some(monty_to_json(&value)),
            print_output: print_out,
            snapshot_handle: None,
            function_name: None,
            os_function: None,
            args: None,
            kwargs: None,
            call_id: None,
            method_call: None,
            pending_call_ids: None,
            error: None,
        },
        RunProgress::FunctionCall(call) => {
            let merged = merge_args(&call.function_name, &call.args, &call.kwargs, param_registry);
            let function_name = call.function_name.clone();
            let call_id = call.call_id;
            let method_call = call.method_call;
            let handle = with_state(|s| {
                let h = s.next_handle();
                s.snapshots.insert(h, SnapshotState::FunctionCall(call));
                h
            });
            ProgressResult {
                status: "function_call",
                value: None,
                snapshot_handle: Some(handle),
                function_name: Some(function_name),
                os_function: None,
                args: Some(merged),
                kwargs: None,
                call_id: Some(call_id),
                method_call: Some(method_call),
                pending_call_ids: None,
                error: None,
                print_output: print_out,
            }
        }
        RunProgress::OsCall(call) => {
            let args_json = monty_args_to_json(&call.args);
            let kwargs_json = monty_kwargs_to_json(&call.kwargs);
            let function_str = call.function.to_string();
            let call_id = call.call_id;
            let handle = with_state(|s| {
                let h = s.next_handle();
                s.snapshots.insert(h, SnapshotState::OsCall(call));
                h
            });
            ProgressResult {
                status: "os_call",
                value: None,
                snapshot_handle: Some(handle),
                function_name: None,
                os_function: Some(function_str),
                args: Some(args_json),
                kwargs: Some(kwargs_json),
                call_id: Some(call_id),
                method_call: None,
                pending_call_ids: None,
                error: None,
                print_output: print_out,
            }
        }
        RunProgress::ResolveFutures(state) => {
            let pending = state.pending_call_ids().to_vec();
            let handle = with_state(|s| {
                let h = s.next_handle();
                s.snapshots.insert(h, SnapshotState::ResolveFutures(state));
                h
            });
            ProgressResult {
                status: "resolve_futures",
                value: None,
                snapshot_handle: Some(handle),
                function_name: None,
                os_function: None,
                args: None,
                kwargs: None,
                call_id: None,
                method_call: None,
                pending_call_ids: Some(pending),
                error: None,
                print_output: print_out,
            }
        }
        // NameLookup should be resolved internally by `drive_progress` before
        // reaching this point. If one slips through we treat it as an error.
        RunProgress::NameLookup(lookup) => ProgressResult {
            status: "error",
            value: None,
            snapshot_handle: None,
            function_name: None,
            os_function: None,
            args: None,
            kwargs: None,
            call_id: None,
            method_call: None,
            pending_call_ids: None,
            error: Some(format!(
                "internal error: unhandled name lookup for '{}'",
                lookup.name
            )),
            print_output: print_out,
        },
    }
}

/// Drives a RunProgress chain, auto-resolving NameLookup events against the
/// registered external function set. Returns a `(status_code, ProgressResult)`
/// pair suitable for the WASM wire format. NameLookup events whose name is
/// present in `param_registry` resolve to a `MontyObject::Function`; unknown
/// names resolve to `Undefined`, which the VM surfaces as `NameError`.
fn drive_progress(
    mut result: Result<RunProgress<LimitedTracker>, MontyException>,
    print_writer: &mut CollectPrintWriter,
    param_registry: &HashMap<String, Vec<String>>,
) -> (u32, ProgressResult) {
    loop {
        match result {
            Ok(RunProgress::NameLookup(lookup)) => {
                let nlr = if param_registry.contains_key(&lookup.name) {
                    NameLookupResult::Value(MontyObject::Function {
                        name: lookup.name.clone(),
                        docstring: None,
                    })
                } else {
                    NameLookupResult::Undefined
                };
                let pw = PrintWriter::Callback(print_writer);
                result = lookup.resume(nlr, pw);
            }
            // A call to a name that's not a registered external function is a
            // NameError — monty's bytecode compiler emits `LoadGlobalCallable`
            // / `LoadLocalCallable` which bypass `NameLookup` and yield a
            // `FunctionCall` directly, so we have to catch the undeclared case
            // here and auto-resume with a NameError exception.
            Ok(RunProgress::FunctionCall(call)) if !param_registry.contains_key(&call.function_name) => {
                let exc = MontyException::new(
                    ExcType::NameError,
                    Some(format!("name '{}' is not defined", call.function_name)),
                );
                let pw = PrintWriter::Callback(print_writer);
                result = call.resume(ExtFunctionResult::Error(exc), pw);
            }
            Ok(progress) => {
                let print_output = print_writer.buf.clone();
                let pr = progress_to_result(progress, print_output, param_registry);
                let status = match pr.status {
                    "complete" => 1,
                    "function_call" => 2,
                    "os_call" => 3,
                    "resolve_futures" => 4,
                    _ => 0,
                };
                return (status, pr);
            }
            Err(e) => {
                let print_output = print_writer.buf.clone();
                return (0, error_result(&e, print_output));
            }
        }
    }
}

fn error_result(err: &MontyException, print_output: String) -> ProgressResult {
    str_error_result(&format!("{err}"), print_output)
}

fn str_error_result(msg: &str, print_output: String) -> ProgressResult {
    let print_out = if print_output.is_empty() {
        None
    } else {
        Some(print_output)
    };
    ProgressResult {
        status: "error",
        value: None,
        snapshot_handle: None,
        function_name: None,
        os_function: None,
        args: None,
        kwargs: None,
        call_id: None,
        method_call: None,
        pending_call_ids: None,
        error: Some(msg.to_owned()),
        print_output: print_out,
    }
}

// ---------------------------------------------------------------------------
// Memory management exports
// ---------------------------------------------------------------------------

#[no_mangle]
pub extern "C" fn wasm_alloc(size: u32) -> u32 {
    if size == 0 {
        return 0;
    }
    let layout = Layout::from_size_align(size as usize, 1).unwrap();
    let ptr = unsafe { alloc(layout) };
    if ptr.is_null() {
        return 0;
    }
    ptr as u32
}

#[no_mangle]
pub extern "C" fn wasm_dealloc(ptr: u32, size: u32) {
    if ptr == 0 || size == 0 {
        return;
    }
    let layout = Layout::from_size_align(size as usize, 1).unwrap();
    unsafe {
        dealloc(ptr as *mut u8, layout);
    }
}

// ---------------------------------------------------------------------------
// Result buffer exports
// ---------------------------------------------------------------------------

#[no_mangle]
pub extern "C" fn monty_result_len() -> u32 {
    RESULT_BUF.lock().unwrap().len() as u32
}

#[no_mangle]
pub extern "C" fn monty_result_read(buf_ptr: u32, buf_cap: u32) -> u32 {
    let result = RESULT_BUF.lock().unwrap();
    let len = result.len().min(buf_cap as usize);
    unsafe {
        std::ptr::copy_nonoverlapping(result.as_ptr(), buf_ptr as *mut u8, len);
    }
    len as u32
}

// ---------------------------------------------------------------------------
// Feasibility check (kept from phase 1)
// ---------------------------------------------------------------------------

#[no_mangle]
pub extern "C" fn monty_check() -> u32 {
    let result = std::panic::catch_unwind(|| {
        let runner = MontyRun::new("x + 1".to_owned(), "check.py", vec!["x".to_owned()])
            .ok()?;
        let result = runner.run_no_limits(vec![MontyObject::Int(41)]).ok()?;
        match result {
            MontyObject::Int(v) => Some(v as u32),
            _ => None,
        }
    });
    match result {
        Ok(Some(v)) => v,
        _ => 0,
    }
}

// ---------------------------------------------------------------------------
// Core API exports
// ---------------------------------------------------------------------------

/// Read a UTF-8 string from WASM linear memory.
unsafe fn read_str(ptr: u32, len: u32) -> String {
    if ptr == 0 || len == 0 {
        return String::new();
    }
    let slice = std::slice::from_raw_parts(ptr as *const u8, len as usize);
    String::from_utf8_lossy(slice).into_owned()
}

/// Parse a JSON array of plain values into Vec<MontyObject>.
fn parse_inputs(json_str: &str) -> Vec<MontyObject> {
    if json_str.is_empty() {
        return vec![];
    }
    let values: Vec<JsonValue> = serde_json::from_str(json_str).unwrap_or_default();
    values.iter().map(json_to_monty).collect()
}

/// Parse a JSON value (return value from Go) into MontyObject.
fn parse_return_value(json_str: &str) -> MontyObject {
    if json_str.is_empty() {
        return MontyObject::None;
    }
    let value: JsonValue = serde_json::from_str(json_str).unwrap_or(JsonValue::Null);
    json_to_monty(&value)
}

/// Compile Python code. Returns a runner handle (>0) on success, 0 on error.
#[no_mangle]
pub extern "C" fn monty_compile(
    code_ptr: u32,
    code_len: u32,
    input_names_ptr: u32,
    input_names_len: u32,
    ext_funcs_ptr: u32,
    ext_funcs_len: u32,
) -> u32 {
    let code = unsafe { read_str(code_ptr, code_len) };
    let input_names_json = unsafe { read_str(input_names_ptr, input_names_len) };
    let ext_funcs_json = unsafe { read_str(ext_funcs_ptr, ext_funcs_len) };

    let input_names: Vec<String> = if input_names_json.is_empty() {
        vec![]
    } else {
        serde_json::from_str(&input_names_json).unwrap_or_default()
    };

    let ext_func_defs: Vec<ExtFuncDef> = if ext_funcs_json.is_empty() {
        vec![]
    } else {
        serde_json::from_str(&ext_funcs_json).unwrap_or_default()
    };

    // Store the external function registry. Names double as the "known
    // externals" set used to resolve NameLookup events — monty >= 0.0.8
    // auto-detects external functions at call sites and yields a NameLookup
    // that the host resolves on demand.
    {
        let mut registry_guard = PARAM_REGISTRY.lock().unwrap();
        let registry = registry_guard.get_or_insert_with(HashMap::new);
        registry.clear();
        for def in &ext_func_defs {
            registry.insert(def.name.clone(), def.params.clone());
        }
    }

    match MontyRun::new(code, "script.py", input_names) {
        Ok(runner) => with_state(|s| {
            let handle = s.next_handle();
            s.runners.insert(handle, runner);
            handle
        }),
        Err(e) => {
            set_result_json(&error_result(&e, String::new()));
            0
        }
    }
}

/// Start execution. Returns a status code:
///   1 = complete, 2 = function_call, 3 = os_call, 4 = resolve_futures, 0 = error
#[no_mangle]
pub extern "C" fn monty_start(
    runner_handle: u32,
    inputs_ptr: u32,
    inputs_len: u32,
    limits_ptr: u32,
    limits_len: u32,
) -> u32 {
    let runner = with_state(|s| s.runners.remove(&runner_handle));
    let runner = match runner {
        Some(r) => r,
        None => {
            set_result_json(&str_error_result("invalid runner handle", String::new()));
            return 0;
        }
    };

    // Parse inputs as plain JSON -> MontyObject.
    let inputs_json = unsafe { read_str(inputs_ptr, inputs_len) };
    let inputs = parse_inputs(&inputs_json);

    // Parse limits.
    let limits_json = unsafe { read_str(limits_ptr, limits_len) };
    let limits_input: LimitsInput = if limits_json.is_empty() {
        LimitsInput {
            max_allocations: None,
            max_duration_ms: None,
            max_memory: None,
            max_recursion_depth: None,
        }
    } else {
        serde_json::from_str(&limits_json).unwrap_or(LimitsInput {
            max_allocations: None,
            max_duration_ms: None,
            max_memory: None,
            max_recursion_depth: None,
        })
    };

    let resource_limits = ResourceLimits {
        max_allocations: limits_input.max_allocations,
        max_duration: limits_input.max_duration_ms.map(Duration::from_millis),
        max_memory: limits_input.max_memory,
        max_recursion_depth: limits_input.max_recursion_depth,
        gc_interval: None,
    };
    let tracker = LimitedTracker::new(resource_limits);

    let mut print_writer = CollectPrintWriter::new();
    let initial = {
        let pw = PrintWriter::Callback(&mut print_writer);
        runner.start(inputs, tracker, pw)
    };

    let (status, result) =
        with_param_registry(|reg| drive_progress(initial, &mut print_writer, reg));
    set_result_json(&result);
    status
}

/// Resume execution after a function call or OS call.
#[no_mangle]
pub extern "C" fn monty_resume(
    snapshot_handle: u32,
    return_value_ptr: u32,
    return_value_len: u32,
) -> u32 {
    let snapshot = with_state(|s| s.snapshots.remove(&snapshot_handle));

    let return_json = unsafe { read_str(return_value_ptr, return_value_len) };
    let return_value = parse_return_value(&return_json);

    let mut print_writer = CollectPrintWriter::new();

    // Dispatch on the stored variant. Each per-variant struct (FunctionCall,
    // OsCall) has its own `resume()` method that consumes self.
    let initial = {
        let pw = PrintWriter::Callback(&mut print_writer);
        match snapshot {
            Some(SnapshotState::FunctionCall(call)) => {
                call.resume(ExtFunctionResult::Return(return_value), pw)
            }
            Some(SnapshotState::OsCall(call)) => {
                call.resume(ExtFunctionResult::Return(return_value), pw)
            }
            _ => {
                set_result_json(&str_error_result("invalid snapshot handle", String::new()));
                return 0;
            }
        }
    };

    let (status, result) =
        with_param_registry(|reg| drive_progress(initial, &mut print_writer, reg));
    set_result_json(&result);
    status
}

/// Resume execution after resolving futures.
#[no_mangle]
pub extern "C" fn monty_resume_futures(
    snapshot_handle: u32,
    results_ptr: u32,
    results_len: u32,
) -> u32 {
    let snapshot = with_state(|s| s.snapshots.remove(&snapshot_handle));
    let snapshot = match snapshot {
        Some(SnapshotState::ResolveFutures(s)) => s,
        _ => {
            set_result_json(&str_error_result(
                "invalid future snapshot handle",
                String::new(),
            ));
            return 0;
        }
    };

    // Parse results as array of [call_id, plain_json_value] pairs.
    let results_json = unsafe { read_str(results_ptr, results_len) };
    let pairs: Vec<(u32, JsonValue)> = if results_json.is_empty() {
        vec![]
    } else {
        serde_json::from_str(&results_json).unwrap_or_default()
    };

    let results: Vec<(u32, ExtFunctionResult)> = pairs
        .into_iter()
        .map(|(id, val)| (id, ExtFunctionResult::Return(json_to_monty(&val))))
        .collect();

    let mut print_writer = CollectPrintWriter::new();
    let initial = {
        let pw = PrintWriter::Callback(&mut print_writer);
        snapshot.resume(results, pw)
    };

    let (status, result) =
        with_param_registry(|reg| drive_progress(initial, &mut print_writer, reg));
    set_result_json(&result);
    status
}

/// Free a runner handle.
#[no_mangle]
pub extern "C" fn monty_free_runner(handle: u32) {
    with_state(|s| {
        s.runners.remove(&handle);
    });
}

/// Free a snapshot handle.
#[no_mangle]
pub extern "C" fn monty_free_snapshot(handle: u32) {
    with_state(|s| {
        s.snapshots.remove(&handle);
    });
}
