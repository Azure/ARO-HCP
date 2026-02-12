#!/bin/bash

# Configuration
export GIT_EDITOR=true
autoresolve_files="config/rendered"

# Function to get list of conflicted files
get_conflicted_files() {
    git status --porcelain | grep "^UU\|^AA\|^DD" | awk '{print $2}'
}

# Function to check if there are any conflicted files
has_conflicts() {
    git status --porcelain | grep -q "^UU\|^AA\|^DD"
}

# Function to check if we're in the middle of a rebase
in_rebase() {
    [ -d ".git/rebase-merge" ] || [ -d ".git/rebase-apply" ]
}

# Change directory to the root of the repo
dir="$(git rev-parse --show-toplevel)"
cd "$dir"

# Check if we're in the middle of a rebase
if in_rebase; then
    echo "Resuming rebase in progress..."
    if git rebase --continue; then
        echo "Rebase completed successfully!"
    else
        echo "Rebase still has conflicts. Please resolve them and run this script again."
        exit 1
    fi
else
    # Start new rebase
    echo "Starting rebase onto main..."
    git rebase --no-verify main
fi

# Handle any remaining conflicts in a loop
while in_rebase; do
    # Check if there are conflicts
    if has_conflicts; then
        echo "Conflicts detected. Resolving allowed files first..."

        # Always resolve allowed files first using 'theirs'
        for file in $(get_conflicted_files); do
            if echo "$autoresolve_files" | grep -q "$file"; then
                echo "Resolving allowed file: $file"
                git checkout --theirs "$file"
                git add "$file"
            fi
        done

        # Check if there are any remaining conflicts after resolving allowed files
        if has_conflicts; then
            echo "Remaining conflicts in files that require manual resolution:"
            get_conflicted_files
            echo "Please resolve these conflicts manually, 'git add' them and run the rebase again."
            exit 1
        fi

        # All conflicts resolved, continue the rebase
        echo "All conflicts resolved. Continuing rebase..."
        git rebase --continue
    else
        # No conflicts, try to continue
        git rebase --continue || break
    fi
done

echo "Rebase completed successfully!"

# Run make to materialize new updates in config
echo "Running 'make -C config materialize'..."
make -C config materialize
echo "Rebase and update completed successfully!"
