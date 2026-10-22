---
name: python-project
description: Set up a new Python project with a venv, requirements.txt, main module, and tests.
---

# Skill: new Python project

Use this when the user wants to start a Python script, tool, or package from scratch.

## Layout
- Create this structure (replace `myproj` with the project name):

  ```
  myproj/
  ├── .venv/            (created by venv, do not hand-edit)
  ├── requirements.txt
  ├── main.py           (entry point)
  ├── tests/
  │   └── test_main.py
  └── README.md         (one paragraph: what it does, how to run)
  ```

- Keep the first version small. One module is fine. Split into a package only when the code grows.

## Virtual environment
- Run `python3 -m venv .venv` inside the project folder.
- Use `.venv/bin/python` and `.venv/bin/pip` for every command after that. Never install into the system Python.
- Tell the user the activation command: `source .venv/bin/activate`.

## Dependencies
- Install only what the code imports: `.venv/bin/pip install requests` etc.
- After installing, freeze them: `.venv/bin/pip freeze > requirements.txt`.
- If there are no third-party imports, create an empty `requirements.txt` with a comment like `# no dependencies yet`.

## Main module
- Put runnable code behind a `main()` function and guard it:

  ```python
  def main():
      ...

  if __name__ == "__main__":
      main()
  ```

- Use `argparse` if the tool takes command-line arguments. Add `--help`-friendly descriptions.
- Write real working code, not stubs. Beginners run what you give them.

## Tests
- If `pytest` is already in the environment or requested, write `tests/test_main.py` with `test_` functions and plain `assert` statements.
- If not, write a plain `tests/test_main.py` script with `assert` checks and a `print("all tests passed")` at the end, runnable with `.venv/bin/python tests/test_main.py`. Do not install pytest just for this.
- Test the important behaviour, not the trivial lines.

## Verify and report
- Run the program once: `.venv/bin/python main.py` (with sample args if needed). Show it works.
- Run the tests. All must pass.
- Finish with a short summary: the files created, and the two commands the user needs: activate the venv, run the program.
