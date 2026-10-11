"""Copyable offline reader argument lists; source text is never shell code."""


def command(script, repository, operation, **arguments):
    name = repository["full_name"] if isinstance(repository, dict) else repository
    result = [f"bin/{script}", "--expected-repo", name]
    if script == "cache" and isinstance(repository, dict):
        result.extend(["--host", repository["host"]])

    result.append(operation)
    for key, value in arguments.items():
        if value is not None:
            result.extend(["--" + key.replace("_", "-"), str(value)])

    return result
