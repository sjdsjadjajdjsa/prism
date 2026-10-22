---
name: python-project
description: Python only: set up a Python project with a venv, requirements.txt, main module, and tests. Not for Node or JavaScript.
---

# Skill: new Python project

Use this when the user wants to start a Python script, tool, or package from scratch.

## Virtual environment
- Create the virtual environment in `.venv`:
  `python3 -m venv .venv`
- If that fails due to missing `ensurepip` (Debian/Ubuntu), fall back to:
  `python3 -m virtualenv .venv` or `python3 -m venv --without-pip .venv`
- Use `.venv/bin/python` and `.venv/bin/pip` for subsequent commands. Do not install into system Python.
- Tell the user the activation command: `source .venv/bin/activate`.

## Project files
- Set entry file to `main.py` with a guarded `main()` function:
  ```python
  def main():
      ...

  if __name__ == "__main__":
      main()
  ```
- Write a short `README.md` explaining what the project does and how to run it.
- Keep dependencies minimal in `requirements.txt` (`.venv/bin/pip freeze > requirements.txt`). Never add test-only deps here.

## Tests
- Plain-assert test files run with `python3 tests/test_x.py` that add the project root to `sys.path`:
  ```python
  import pathlib, sys
  sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))
  ```
- OR if pytest is already installed in the environment, add an empty `conftest.py` at the root so `pytest` works directly.

## Verify and report
- Run the entry point: `.venv/bin/python main.py`.
- Run the tests (`python3 tests/test_x.py` or `pytest`).
- End with a very short summary: files created, how to activate the venv, and how to run the project.
