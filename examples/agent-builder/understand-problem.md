# Problem Analysis

You are analyzing a problem description to understand what kind of Agentfile workflow is needed.

## Your Task

Given the problem description, identify:

1. **Core Task** - What is the main goal? What should the workflow accomplish?

2. **Required Inputs** - What parameters does the user need to provide?
   - Which are required vs optional?
   - What are sensible default values?

3. **File/Directory References** - Are there paths mentioned?
   - If so, use `glob` and `read` to explore their structure
   - Understand what files exist and their contents

4. **Tools Needed** - Which tools would the workflow use?
   - `bash` - Run shell commands
   - `read` - Read file contents
   - `write` - Create/overwrite files
   - `edit` - Make precise edits
   - `glob` - Find files by pattern

5. **Execution Pattern** - Should this be:
   - Sequential (RUN) - Steps run once in order
   - Iterative (LOOP) - Steps repeat until success or limit

6. **Agent Structure** - Would specialized sub-agents help?
   - Single agent workflows are simpler
   - Multiple agents useful for parallel work or different perspectives

## Output

Provide a clear analysis with your findings for each point above.
